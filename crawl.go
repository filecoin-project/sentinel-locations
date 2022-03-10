package main

import (
	"context"
	"log"
	"sync"
	"time"

	connmgr "github.com/libp2p/go-libp2p-connmgr"

	ds "github.com/ipfs/go-datastore"
	dsync "github.com/ipfs/go-datastore/sync"
	libp2p "github.com/libp2p/go-libp2p"
	noise "github.com/libp2p/go-libp2p-noise"

	crypto "github.com/libp2p/go-libp2p-core/crypto"
	host "github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/peer"
	libp2ptls "github.com/libp2p/go-libp2p-tls"

	ipns "github.com/ipfs/go-ipns"
	routing "github.com/libp2p/go-libp2p-core/routing"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	record "github.com/libp2p/go-libp2p-record"

	multiaddr "github.com/multiformats/go-multiaddr"

	protocol "github.com/libp2p/go-libp2p-core/protocol"
)

type NetworkName string

const (
	Mainnet  NetworkName = "mainnet"
	Calibnet NetworkName = "calibnet"
)

var MainnetPeers = []string{
	"/dns4/bootstrap-0.mainnet.filops.net/tcp/1347/p2p/12D3KooWCVe8MmsEMes2FzgTpt9fXtmCY7wrq91GRiaC8PHSCCBj",
	"/dns4/bootstrap-1.mainnet.filops.net/tcp/1347/p2p/12D3KooWCwevHg1yLCvktf2nvLu7L9894mcrJR4MsBCcm4syShVc",
	"/dns4/bootstrap-2.mainnet.filops.net/tcp/1347/p2p/12D3KooWEWVwHGn2yR36gKLozmb4YjDJGerotAPGxmdWZx2nxMC4",
	"/dns4/bootstrap-3.mainnet.filops.net/tcp/1347/p2p/12D3KooWKhgq8c7NQ9iGjbyK7v7phXvG6492HQfiDaGHLHLQjk7R",
	"/dns4/bootstrap-4.mainnet.filops.net/tcp/1347/p2p/12D3KooWL6PsFNPhYftrJzGgF5U18hFoaVhfGk7xwzD8yVrHJ3Uc",
	"/dns4/bootstrap-5.mainnet.filops.net/tcp/1347/p2p/12D3KooWLFynvDQiUpXoHroV1YxKHhPJgysQGH2k3ZGwtWzR4dFH",
	"/dns4/bootstrap-6.mainnet.filops.net/tcp/1347/p2p/12D3KooWP5MwCiqdMETF9ub1P3MbCvQCcfconnYHbWg6sUJcDRQQ",
	"/dns4/bootstrap-7.mainnet.filops.net/tcp/1347/p2p/12D3KooWRs3aY1p3juFjPy8gPN95PEQChm2QKGUCAdcDCC4EBMKf",
	"/dns4/bootstrap-8.mainnet.filops.net/tcp/1347/p2p/12D3KooWScFR7385LTyR4zU1bYdzSiiAb5rnNABfVahPvVSzyTkR",
	"/dns4/lotus-bootstrap.ipfsforce.com/tcp/41778/p2p/12D3KooWGhufNmZHF3sv48aQeS13ng5XVJZ9E6qy2Ms4VzqeUsHk",
	"/dns4/bootstrap-0.starpool.in/tcp/12757/p2p/12D3KooWGHpBMeZbestVEWkfdnC9u7p6uFHXL1n7m1ZBqsEmiUzz",
	"/dns4/bootstrap-1.starpool.in/tcp/12757/p2p/12D3KooWQZrGH1PxSNZPum99M1zNvjNFM33d1AAu5DcvdHptuU7u",
	"/dns4/node.glif.io/tcp/1235/p2p/12D3KooWBF8cpp65hp2u9LK5mh19x67ftAam84z9LsfaquTDSBpt",
	"/dns4/bootstrap-0.ipfsmain.cn/tcp/34721/p2p/12D3KooWQnwEGNqcM2nAcPtRR9rAX8Hrg4k9kJLCHoTR5chJfz6d",
	"/dns4/bootstrap-1.ipfsmain.cn/tcp/34723/p2p/12D3KooWMKxMkD5DMpSWsW7dBddKxKT7L2GgbNuckz9otxvkvByP",
}

var CalibnetPeers = []string{
	"/dns4/bootstrap-0.calibration.fildev.network/tcp/1347/p2p/12D3KooWJkikQQkxS58spo76BYzFt4fotaT5NpV2zngvrqm4u5ow",
	"/dns4/bootstrap-1.calibration.fildev.network/tcp/1347/p2p/12D3KooWLce5FDHR4EX4CrYavphA5xS3uDsX6aoowXh5tzDUxJav",
	"/dns4/bootstrap-2.calibration.fildev.network/tcp/1347/p2p/12D3KooWA9hFfQG9GjP6bHeuQQbMD3FDtZLdW1NayxKXUT26PQZu",
	"/dns4/bootstrap-3.calibration.fildev.network/tcp/1347/p2p/12D3KooWMHDi3LVTFG8Szqogt7RkNXvonbQYqSazxBx41A5aeuVz",
}

type FilecoinPeer struct {
	ctx            context.Context
	dht            *dht.IpfsDHT
	host           host.Host
	bootstrapPeers []peer.AddrInfo
}

func FilecoinBootstrapPeers(peerList []string) []peer.AddrInfo {
	if peerList == nil {
		peerList = MainnetPeers
	}

	maddrs := make([]multiaddr.Multiaddr, len(peerList))

	for i, bp := range peerList {
		var err error
		maddrs[i], err = multiaddr.NewMultiaddr(bp)
		if err != nil {
			return nil
		}
	}

	peers, _ := peer.AddrInfosFromP2pAddrs(maddrs...)

	return peers
}

func SetupFilecoinPeer(
	ctx context.Context,
	network NetworkName,
) (*FilecoinPeer, error) {
	if network == "" {
		network = Mainnet
	}

	var pp protocol.ID
	var bootstrapPeers []peer.AddrInfo

	switch network {
	case Mainnet:
		pp = "/fil/kad/testnetnet"
		bootstrapPeers = FilecoinBootstrapPeers(MainnetPeers)
	case Calibnet:
		pp = "/fil/kad/calibnet"
		bootstrapPeers = FilecoinBootstrapPeers(CalibnetPeers)
	}

	var ddht *dht.IpfsDHT
	priv, _, err := crypto.GenerateKeyPair(crypto.RSA, 2048)

	if err != nil {
		return &FilecoinPeer{}, err
	}

	libp2pOpts := []libp2p.Option{
		libp2p.ListenAddrStrings(
			"/ip4/0.0.0.0/tcp/9000",      // regular tcp connections
			"/ip4/0.0.0.0/udp/9000/quic", // a UDP endpoint for the QUIC transport
		),
		libp2p.Identity(priv),
		libp2p.Security(libp2ptls.ID, libp2ptls.New),
		libp2p.Security(noise.ID, noise.New),
		libp2p.DefaultTransports,
		libp2p.Routing(func(h host.Host) (routing.PeerRouting, error) {
			ddht, err := dht.New(
				ctx,
				h,
				dht.Option(dht.Datastore(dsync.MutexWrap(ds.NewMapDatastore()))),
				dht.Option(dht.NamespacedValidator("pk", record.PublicKeyValidator{})),
				dht.Option(dht.NamespacedValidator("ipns", ipns.Validator{KeyBook: h.Peerstore()})),
				dht.Option(dht.Concurrency(10)),
				dht.Option(dht.Mode(dht.ModeAuto)),
				dht.Option(dht.ProtocolPrefix(pp)),
			)

			return ddht, err
		}),
		libp2p.NATPortMap(),
		libp2p.ConnectionManager(connmgr.NewConnManager(100, 600, time.Minute)),
		libp2p.EnableAutoRelay(),
		libp2p.EnableNATService(),
	}

	h, err := libp2p.New(libp2pOpts...)

	if err != nil {
		return &FilecoinPeer{}, err
	}

	return &FilecoinPeer{ctx: ctx, host: h, dht: ddht, bootstrapPeers: bootstrapPeers}, nil
}

func (f *FilecoinPeer) Bootstrap() {
	connected := make(chan struct{})

	var wg sync.WaitGroup
	for _, pinfo := range f.bootstrapPeers {
		wg.Add(1)
		go func(p peer.AddrInfo) {
			defer wg.Done()
			err := f.host.Connect(f.ctx, p)
			if err != nil {
				log.Println(err)
				return
			}
			log.Println("Connected to ", p.ID)
			connected <- struct{}{}
		}(pinfo)
	}

	go func() {
		wg.Wait()
		close(connected)
	}()
}

func (f *FilecoinPeer) Crawl(d time.Duration) {
	// connect to bootstrap peers
	go f.Bootstrap()

	// use ticker instead of sleep to make up for slow receivers
	stop := make(chan struct{}, 1)
	stopTicker := time.NewTicker(d)

	statusTicker := time.NewTicker(time.Duration(5) * time.Second)

	for {
		select {
		case <-statusTicker.C:
			log.Printf("found peers: %v\n", len(f.host.Peerstore().Peers()))
		case <-stopTicker.C:
			stop <- struct{}{}
			return
		}
	}
}
