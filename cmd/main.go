package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	drouting "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	dutil "github.com/libp2p/go-libp2p/p2p/discovery/util"
	"os"
	"sync"
	"time"
)

// DiscoveryInterval is how often we re-publish our mDNS records.
const DiscoveryInterval = time.Hour

// DiscoveryServiceTag is used in our mDNS advertisements to discover other chat peers.
const DiscoveryServiceTag = "FIL_BANGALORE_CHAT_APP"

func main() {
	// parse some flags to set our nickname and the room to join
	nickFlag := flag.String("nick", "", "nickname to use in chat. will be generated if empty")
	roomFlag := flag.String("room", "awesome-chat-room", "name of chat room to join")
	flag.Parse()

	ctx := context.Background()

	// EXPLAIN TRANSPORT
	// WE USE QUICK SO WE GET MULTIPLEXING AND SECURE CHANNEL OUT OF THE BOX
	// create a new libp2p Host that listens on a random UDP port for QUIC
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0/"))
	if err != nil {
		panic(err)
	}
	fmt.Println("Host created. We are:", h.ID())
	fmt.Println("Listening on:", h.Addrs())

	// WORKSHOP NOTE: EXPLAIN PUBSUB AND GOSSIPSUB and point to paper
	// create a new PubSub service using the GossipSub router
	// https://github.com/libp2p/specs/tree/master/pubsub/gossipsub
	// https://research.protocol.ai/blog/2019/a-new-lab-for-resilient-networks-research/PL-TechRep-gossipsub-v0.1-Dec30.pdf
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		panic(err)
	}

	// EXPLAIN MDNS OVERVIEW
	// setup local mDNS discovery
	d := &dhtdiscovery{h: h}
	d.discover(ctx)

	// use the nickname from the cli flag, or a default if blank
	nick := *nickFlag
	if len(nick) == 0 {
		nick = defaultNick(h.ID())
	}

	// join the room from the cli flag, or the flag default
	room := *roomFlag

	// join the chat room
	cr, err := JoinChatRoom(ctx, ps, h.ID(), nick, room)
	if err != nil {
		panic(err)
	}

	// draw the UI
	ui := NewChatUI(cr)
	if err = ui.Run(); err != nil {
		printErr("error running text UI: %s", err)
	}
}

// printErr is like fmt.Printf, but writes to stderr.
func printErr(m string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, m, args...)
}

// defaultNick generates a nickname based on the $USER environment variable and
// the last 8 chars of a peer ID.
func defaultNick(p peer.ID) string {
	return fmt.Sprintf("%s-%s", os.Getenv("USER"), shortID(p))
}

// shortID returns the last 8 chars of a base58-encoded peer id.
func shortID(p peer.ID) string {
	pretty := p.String()
	return pretty[len(pretty)-8:]
}

type dhtdiscovery struct {
	h host.Host
}

func (discovery *dhtdiscovery) discover(ctx context.Context) {
	d, err := dht.New(ctx, discovery.h, dht.BootstrapPeers(dht.GetDefaultBootstrapPeerAddrInfos()...))
	if err != nil {
		panic(err)
	}
	if err := d.Bootstrap(ctx); err != nil {
		panic(err)
	}
	fmt.Println("\n DHT boostrapped")

	for _, pi := range dht.GetDefaultBootstrapPeerAddrInfos() {
		if err := discovery.h.Connect(ctx, pi); err != nil {
			continue
		}
		fmt.Println("\n Connection established with bootstrap node:", pi.ID)
	}

	routingDiscovery := drouting.NewRoutingDiscovery(d)
	dutil.Advertise(ctx, routingDiscovery, DiscoveryServiceTag)

	time.Sleep(45 * time.Second)
	peerChan, err := routingDiscovery.FindPeers(ctx, DiscoveryServiceTag)
	if err != nil {
		panic(err)
	}

	var mu sync.Mutex
	ms := make(map[peer.ID]bool)

	for peer := range peerChan {
		if peer.ID == discovery.h.ID() {
			continue
		}
		if err := discovery.h.Connect(ctx, peer); err != nil {
			continue
		}
		fmt.Println("\n Found peer:", peer.ID)

		mu.Lock()
		ms[peer.ID] = true
		mu.Unlock()
	}

	go func() {
		for {
			time.Sleep(30 * time.Second)
			peerChan, err := routingDiscovery.FindPeers(ctx, DiscoveryServiceTag)
			if err != nil {
				panic(err)
			}

			for peer := range peerChan {
				if peer.ID == discovery.h.ID() {
					continue
				}

				mu.Lock()
				if _, ok := ms[peer.ID]; ok {
					continue
				}
				mu.Unlock()

				if err := discovery.h.Connect(ctx, peer); err != nil {
					continue
				}
				fmt.Println("\n Found peer:", peer.ID)

				mu.Lock()
				ms[peer.ID] = true
				mu.Unlock()
			}
		}
	}()
}
