package main

import (
	"context"
	_ "embed"
	"log"
	"os"
	"sync"
	"time"

	crypto "github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/peer"

	pg "github.com/go-pg/pg/v10"
	orm "github.com/go-pg/pg/v10/orm"

	ipfsgeoip "github.com/hsanjuan/go-ipfs-geoip"
	ipfslite "github.com/hsanjuan/ipfs-lite"

	multiaddr "github.com/multiformats/go-multiaddr"
	madns "github.com/multiformats/go-multiaddr-dns"
)

const schema = "locations"

//go:embed query.sql
var query string

//go:embed active_miners.sql
var activeMinersQuery string

type minerInfo struct {
	//lint:ignore U1000 hit for go-pg
	tableName      struct{} `pg:"locations.miners"`
	Height         int64    `pg:",pk,notnull"` // PK is Height + MinerID
	MinerID        string   `pg:",pk,notnull"`
	MultiAddresses []string `pg:"-"`
	CountryName    string   `pg:",notnull"`
	Latitude       int      `pg:",notnull"`
	Longitude      int      `pg:",notnull"`
	Source         string   `pg:","`
	PeerID         string   `pg:"-"`
}

type minerInfos []minerInfo

// Persist uses a transaction to insert multiple quotes in the DB.
func (minfos minerInfos) Persist(ctx context.Context, tx *pg.Tx) error {
	if len(minfos) == 0 {
		return nil
	}

	_, err := tx.ModelContext(ctx, &minfos).
		OnConflict("(height, miner_id) DO UPDATE").
		Insert()
	return err
}

func main() {
	ctx := context.Background()

	db, err := connectToDB()
	if err != nil {
		log.Println("error connecting to db")
		log.Fatal(err)
	}
	defer db.Close()

	fp, err := getRouter(ctx)
	if err != nil {
		log.Println("error setting up FilecoinPeer")
		log.Fatal(err)
	}
	defer fp.host.Close()

	// This bootstraps IPFS while we get miner infos.
	loc, err := getLocator(ctx)
	if err != nil {
		log.Println("error setting up ipfs-geoip lookups")
		log.Fatal(err)
	}

	infosFromDHT := getActiveMiners(db, fp)
	if err != nil {
		log.Println("error obtaining list of miner infos from dht")
		log.Fatal(err)
	}
	log.Printf("found %d miners with defined addresses from dht", len(infosFromDHT))

	infos, err := getMultiAddrsFromMinerInfos(db)
	if err != nil {
		log.Println("error obtaining list of miner infos")
		log.Fatal(err)
	}
	log.Printf("found %d miners with defined addresses", len(infos))

	for info := range infosFromDHT {
		infos = append(infos, info)
	}

	infos, err = lookupLocations(ctx, loc, infos)
	if err != nil {
		log.Println("error looking up locations")
		log.Fatal(err)
	}
	log.Printf("found location information for %d miners", len(infos))

	err = insert(ctx, db, infos)
	if err != nil {
		log.Println("error inserting miner location information")
		log.Fatal(err)
	}

}

func connectToDB() (*pg.DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opts, err := pg.ParseURL(os.Getenv("SENTINEL_DB"))
	if err != nil {
		return nil, err
	}

	db := pg.Connect(opts)

	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, err
	}

	if _, err = db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema+";"); err != nil {
		db.Close()
		return nil, err
	}

	err = db.Model(&minerInfo{}).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	})
	if err != nil {
		db.Close()
		return nil, err
	}

	if _, err = db.ExecContext(
		ctx,
		"ALTER TABLE "+schema+".miners ADD COLUMN IF NOT EXISTS source VARCHAR(24);",
	); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func getLocator(ctx context.Context) (*ipfsgeoip.IPLocator, error) {
	ds := ipfslite.NewInMemoryDatastore()
	priv, _, err := crypto.GenerateKeyPair(crypto.RSA, 2048)

	if err != nil {
		return nil, err
	}

	h, dht, err := ipfslite.SetupLibp2p(
		ctx,
		priv,
		nil,
		nil,
		ds,
		ipfslite.Libp2pOptionsExtra...,
	)

	if err != nil {
		return nil, err
	}

	lite, err := ipfslite.New(ctx, ds, h, dht, nil)
	if err != nil {
		return nil, err
	}

	go lite.Bootstrap(ipfslite.DefaultBootstrapPeers())

	return ipfsgeoip.NewIPLocator(lite.Session(ctx)), nil
}

func getRouter(ctx context.Context) (*filecoinPeer, error) {
	fp, err := setupFilecoinPeer(
		ctx,
		"mainnet",
	)

	if err != nil {
		return &filecoinPeer{}, nil
	}

	return fp, nil
}

func getActiveMiners(db *pg.DB, fp *filecoinPeer) <-chan minerInfo {
	go fp.bootstrap()

	// get active miners in last two weeks, i.e.
	// miners making power claims
	var activeMiners []minerInfo
	_, err := db.Query(&activeMiners, activeMinersQuery)
	if err != nil {
		return nil
	}

	found := make(chan minerInfo)
	var wg sync.WaitGroup

	for i, activeMiner := range activeMiners {
		wg.Add(1)

		go func(i int, am minerInfo) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(15*time.Second))
			defer cancel()

			decodedPid, _ := peer.Decode(am.PeerID)
			info, err := fp.dht.FindPeer(ctx, decodedPid)
			if err != nil {
				log.Println("could not find peer for ", am.PeerID)
			} else {
				peerAddrs := info.Addrs
				peerAddrsAsStr := make([]string, len(peerAddrs))
				for _, p := range peerAddrs {
					peerAddrsAsStr = append(peerAddrsAsStr, p.String())
				}
				activeMiners[i].MultiAddresses = peerAddrsAsStr
				activeMiners[i].Source = "dht"
			}

			found <- activeMiners[i]
		}(i, activeMiner)
	}

	go func() {
		wg.Wait()
		close(found)
	}()

	return found
}

func getMultiAddrsFromMinerInfos(db *pg.DB) ([]minerInfo, error) {
	// Tl;dr: get latest known miner_id, multi_addresses, height for each
	// miner.
	var infos []minerInfo
	_, err := db.Query(&infos, query)
	if err != nil {
		return nil, err
	}

	for i := range infos {
		infos[i].Source = "self-reported"
	}

	return infos, nil
}

// We make the assumption that it does not make sense if a miner reports IPs
// in multiple locations. Therefore, we take the first "resolved" location as
// the valid one.
func lookupLocations(ctx context.Context, loc *ipfsgeoip.IPLocator, infos minerInfos) (minerInfos, error) {
	var locatedMiners minerInfos

	for i, info := range infos {
		log.Println(info)
		for _, addr := range info.MultiAddresses {
			ma, err := multiaddr.NewMultiaddr(addr)
			if err != nil {
				log.Println("error parsing multiaddress: ", err)
				continue
			}
			resolved, err := resolveMultiaddr(ctx, ma)
			if err != nil {
				//log.Println("error resolving ", ma, info.MinerID)
				continue
			}

			for _, r := range resolved {
				ipv4, errIP4 := r.ValueForProtocol(multiaddr.P_IP4)
				if errIP4 != nil {
					//log.Println("no ip4s found for ", info.MinerID)
					continue
				}

				geo, err := lookup(ctx, loc, ipv4)
				if err != nil {
					//log.Println("error looking up country for ", info.MinerID, err, ipv4)
					continue
				}
				if geo.CountryName == "" {
					// keep trying
					continue
				}
				info.CountryName = geo.CountryName
				info.Latitude = int(geo.Latitude * 1000000)
				info.Longitude = int(geo.Longitude * 1000000)
				locatedMiners = append(locatedMiners, info)
				break
			}
			// We found a country. Move to next miner.
			if info.CountryName != "" {
				break // Break multiaddresses loop. Back to infos loop
			}
		}
		if i%100 == 0 {
			log.Printf("Completed geo-lookup for %d out of %d (success on %d)", i+1, len(infos), len(locatedMiners))
		}
	}
	return locatedMiners, nil
}

func resolveMultiaddr(ctx context.Context, ma multiaddr.Multiaddr) ([]multiaddr.Multiaddr, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return madns.Resolve(ctx, ma)
}

func lookup(ctx context.Context, loc *ipfsgeoip.IPLocator, ip string) (ipfsgeoip.GeoIPInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	return loc.LookUp(ctx, ip)
}

func insert(ctx context.Context, db *pg.DB, infos minerInfos) error {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	return db.RunInTransaction(ctx2, func(tx *pg.Tx) error {
		return infos.Persist(ctx, tx)
	})
}
