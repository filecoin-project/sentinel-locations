package main

import (
	"context"
	"sync"
	"time"

	ds "github.com/ipfs/go-datastore"
	dsync "github.com/ipfs/go-datastore/sync"
	libp2p "github.com/libp2p/go-libp2p"
	noise "github.com/libp2p/go-libp2p-noise"

	crypto "github.com/libp2p/go-libp2p-core/crypto"
	host "github.com/libp2p/go-libp2p-core/host"
	peer "github.com/libp2p/go-libp2p-core/peer"
	libp2ptls "github.com/libp2p/go-libp2p-tls"

	ipns "github.com/ipfs/go-ipns"
	routing "github.com/libp2p/go-libp2p-core/routing"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	record "github.com/libp2p/go-libp2p-record"

	multiaddr "github.com/multiformats/go-multiaddr"

	protocol "github.com/libp2p/go-libp2p-core/protocol"
)

type networkName string

const (
	mainnet  networkName = "mainnet"
	calibnet networkName = "calibnet"
)

var mainnetPeers = []string{
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

var calibnetPeers = []string{
	"/dns4/bootstrap-0.calibration.fildev.network/tcp/1347/p2p/12D3KooWJkikQQkxS58spo76BYzFt4fotaT5NpV2zngvrqm4u5ow",
	"/dns4/bootstrap-1.calibration.fildev.network/tcp/1347/p2p/12D3KooWLce5FDHR4EX4CrYavphA5xS3uDsX6aoowXh5tzDUxJav",
	"/dns4/bootstrap-2.calibration.fildev.network/tcp/1347/p2p/12D3KooWA9hFfQG9GjP6bHeuQQbMD3FDtZLdW1NayxKXUT26PQZu",
	"/dns4/bootstrap-3.calibration.fildev.network/tcp/1347/p2p/12D3KooWMHDi3LVTFG8Szqogt7RkNXvonbQYqSazxBx41A5aeuVz",
}

type filecoinPeer struct {
	ctx            context.Context
	dht            *dht.IpfsDHT
	host           host.Host
	bootstrapPeers []peer.AddrInfo
	foundMiners    []peer.AddrInfo
}

type minerWithDecodedPeerID struct {
	miner         minerInfo
	decodedPeerID peer.ID
}

func filecoinBootstrapPeers(peerList []string) []peer.AddrInfo {
	if peerList == nil {
		peerList = mainnetPeers
	}

	maddrs := make([]multiaddr.Multiaddr, len(peerList))

	for i, bp := range peerList {
		var err error
		maddrs[i], err = multiaddr.NewMultiaddr(bp)
		if err != nil {
			log.Fatal(err)
			continue
		}
	}

	peers, _ := peer.AddrInfosFromP2pAddrs(maddrs...)

	return peers
}

func setupFilecoinPeer(
	ctx context.Context,
	network networkName,
) (*filecoinPeer, error) {
	if network == "" {
		network = mainnet
	}

	var pp protocol.ID
	var bootstrapPeers []peer.AddrInfo

	switch network {
	case mainnet:
		pp = "/fil/kad/testnetnet"
		bootstrapPeers = filecoinBootstrapPeers(mainnetPeers)
	case calibnet:
		pp = "/fil/kad/calibnet"
		bootstrapPeers = filecoinBootstrapPeers(calibnetPeers)
	}

	var ddht *dht.IpfsDHT
	priv, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)

	if err != nil {
		return &filecoinPeer{}, err
	}

	libp2pOpts := []libp2p.Option{
		libp2p.Identity(priv),
		libp2p.Security(libp2ptls.ID, libp2ptls.New),
		libp2p.Security(noise.ID, noise.New),
		libp2p.DefaultTransports,
		libp2p.Routing(func(h host.Host) (routing.PeerRouting, error) {
			ddht, err = dht.New(
				ctx,
				h,
				dht.Datastore(dsync.MutexWrap(ds.NewMapDatastore())),
				dht.NamespacedValidator("pk", record.PublicKeyValidator{}),
				dht.NamespacedValidator("ipns", ipns.Validator{KeyBook: h.Peerstore()}),
				dht.Concurrency(50),
				dht.Mode(dht.ModeClient),
				dht.ProtocolPrefix(pp),
			)

			return ddht, err
		}),
	}

	h, err := libp2p.New(libp2pOpts...)

	if err != nil {
		return &filecoinPeer{}, err
	}

	return &filecoinPeer{ctx: ctx, host: h, dht: ddht, bootstrapPeers: bootstrapPeers}, nil
}

func (f *filecoinPeer) bootstrap() error {
	connected := make(chan struct{})

	var wg sync.WaitGroup
	for _, pinfo := range f.bootstrapPeers {
		p := pinfo
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := f.host.Connect(f.ctx, p)
			if err != nil {
				log.Warn(err)
				return
			}
			log.Infow("Connected", "ID", p.ID)
			connected <- struct{}{}
		}()
	}

	go func() {
		wg.Wait()
		close(connected)
	}()

	err := f.dht.Bootstrap(f.ctx)
	if err != nil {
		return err
	}

	return nil
}

func (f *filecoinPeer) findPeersWithDHT(ctx context.Context, m minerWithDecodedPeerID) (minerInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(5*time.Second))
	defer cancel()

	info, err := f.dht.FindPeer(ctx, m.decodedPeerID)
	if err != nil {
		log.Warnw("could not find peer for ", "ID", m.decodedPeerID, "error", err)
		return minerInfo{}, err
	} else {
		peerAddrs := info.Addrs
		peerAddrsAsStr := make([]string, len(peerAddrs))
		for i, p := range peerAddrs {
			peerAddrsAsStr[i] = p.String()
		}
		m.miner.MultiAddresses = peerAddrsAsStr
		m.miner.Source = "dht"
		return m.miner, nil
	}
}
