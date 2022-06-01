package main

import (
	"context"
	_ "embed"
	"github.com/libp2p/go-libp2p-core/peer"
	"golang.org/x/sync/semaphore"
	"os"
	"runtime"
	"sync"
	"time"

	pg "github.com/go-pg/pg/v10"
	orm "github.com/go-pg/pg/v10/orm"

	ipfsgeoip "github.com/hsanjuan/go-ipfs-geoip"
	ipfslite "github.com/hsanjuan/ipfs-lite"

	multiaddr "github.com/multiformats/go-multiaddr"
	madns "github.com/multiformats/go-multiaddr-dns"

	logging "github.com/ipfs/go-log/v2"
	crypto "github.com/libp2p/go-libp2p-core/crypto"
)

var log = logging.Logger("sentinel-locations")

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
		log.Fatalw("error connecting to db", "error", err)
	}
	defer db.Close()

	fp, err := getRouter(ctx)
	if err != nil {
		log.Fatalw("error setting up FilecoinPeer", "error", err)
	}
	defer fp.host.Close()

	// This bootstraps IPFS while we get miner infos.
	loc, err := getLocator(ctx)
	if err != nil {
		log.Fatalw("error setting up ipfs-geoip lookups", "error", err)
	}

	infosFromDHT, err := getActiveMiners(db, fp)
	if err != nil {
		log.Fatalw("error obtaining list of miner infos from dht", "error", err)
	}

	infos, err := getMultiAddrsFromMinerInfos(db)
	if err != nil {
		log.Fatalw("error obtaining list of miner infos", "error", err)
	}

	for _, info := range infosFromDHT {
		infos = append(infos, info)
	}

	infos, err = lookupLocations(ctx, loc, infos)
	if err != nil {
		log.Fatalw("error looking up locations", "error", err)
	}
	log.Info("found location information for %d miners", len(infos))

	err = insert(ctx, db, infos)
	if err != nil {
		log.Fatalw("error inserting miner location information", "error", err)
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
		return nil, err
	}

	return fp, nil
}

func getActiveMiners(db *pg.DB, fp *filecoinPeer) ([]minerInfo, error) {
	if err := fp.bootstrap(); err != nil {
		log.Fatalw("error bootstrapping dht", err)
	}

	// get active miners in last two weeks, i.e.
	// miners making power claims
	// access concurrently
	var activeMiners []minerInfo
	_, err := db.Query(&activeMiners, activeMinersQuery)
	if err != nil {
		return nil, err
	}

	var (
		found      = make([]minerInfo, len(activeMiners))
		peerIDs    = make(chan minerWithDecodedPeerID, 50)
		wg         = sync.WaitGroup{}
		maxWorkers = runtime.GOMAXPROCS(0)
		sem        = semaphore.NewWeighted(int64(maxWorkers))
	)
	for i := range activeMiners {

		miner := activeMiners[i]

		decodedPid, err := peer.Decode(miner.PeerID)
		if err != nil {
			log.Warnw("could not decode peer ID ", "ID", miner.PeerID, "error", err)
		}
		peerIDs <- minerWithDecodedPeerID{miner: miner, decodedPeerID: decodedPid}

		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sem.Acquire(fp.ctx, 1); err != nil {
				log.Warnw("Failed to acquire semaphore: ", err)
			}
			defer sem.Release(1)
			f, err := fp.findPeersWithDHT(<-peerIDs)
			if err != nil {
				return
			}
			found = append(found, f)
		}()
	}

	go func() {
		wg.Wait()
	}()
	if err := sem.Acquire(fp.ctx, int64(maxWorkers)); err != nil {
		log.Warnw("Failed to acquire semaphore: %v", err)
	}
	return found, nil
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
		for _, addr := range info.MultiAddresses {
			ma, err := multiaddr.NewMultiaddr(addr)
			if err != nil {
				log.Warnw("error parsing multiaddress", "error", err)
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
			log.Infof("Completed geo-lookup for %d out of %d (success on %d)", i+1, len(infos), len(locatedMiners))
		}
	}
	log.Infof("found location information for %d miners", len(infos))
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
