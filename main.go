package main

import (
	"context"
	_ "embed"
	"log"
	"os"
	"time"

	pg "github.com/go-pg/pg/v10"
	orm "github.com/go-pg/pg/v10/orm"
	ipfsgeoip "github.com/hsanjuan/go-ipfs-geoip"
	ipfslite "github.com/hsanjuan/ipfs-lite"
	crypto "github.com/libp2p/go-libp2p-core/crypto"
	multiaddr "github.com/multiformats/go-multiaddr"
	madns "github.com/multiformats/go-multiaddr-dns"
)

const schema = "locations"

//go:embed query.sql
var query string

type minerInfo struct {
	//lint:ignore U1000 hit for go-pg
	tableName      struct{} `pg:"locations.miners"`
	Height         int64    `pg:",pk,notnull"` // PK is Height + MinerID
	MinerID        string   `pg:",pk,notnull"`
	MultiAddresses []string `pg:"-"`
	CountryName    string   `pg:",notnull"`
	Latitude       int      `pg:",notnull"`
	Longitude      int      `pg:",notnull"`
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

	// This bootstraps IPFS while we get miner infos.
	loc, err := getLocator(ctx)
	if err != nil {
		log.Println("error setting up ipfs-geoip lookups")
		log.Fatal(err)
	}

	infos, err := getMinerInfos(db)
	if err != nil {
		log.Println("error obtaining list of miner infos")
		log.Fatal(err)
	}
	log.Printf("found %d miners with defined addresses", len(infos))

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

	// An exchange session will speed things up
	return ipfsgeoip.NewIPLocator(lite.Session(ctx)), nil
}

func getMinerInfos(db *pg.DB) ([]minerInfo, error) {
	// Tl;dr: get latest known miner_id, multi_addresses, height for each
	// miner.
	var infos []minerInfo
	_, err := db.Query(&infos, query)
	if err != nil {
		return nil, err
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
