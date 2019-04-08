package posnode

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Fantom-foundation/go-lachesis/src/crypto"
	"github.com/Fantom-foundation/go-lachesis/src/hash"
)

func TestDiscovery(t *testing.T) {
	// node 1
	store1 := NewMemStore()
	node1 := NewForTests("node1", store1, nil)
	node1.StartServiceForTests()
	defer node1.StopService()

	// node 2
	store2 := NewMemStore()
	node2 := NewForTests("node2", store2, nil)

	// connect node2 to node1
	store2.BootstrapPeers(&Peer{
		ID:     node1.ID,
		PubKey: node1.pub,
		Host:   node1.host,
	})
	node2.initPeers()

	t.Run("ask for unknown", func(t *testing.T) {
		assert := assert.New(t)

		unknown := hash.FakePeer()
		node2.AskPeerInfo(node1.ID, node1.host, &unknown)

		peer := store2.GetPeer(unknown)
		assert.Nil(peer)
	})

	t.Run("ask for himself", func(t *testing.T) {
		assert := assert.New(t)

		unknown := node1.ID
		node2.AskPeerInfo(node1.ID, node1.host, &unknown)

		peer := store2.GetPeer(unknown)
		assert.Equal(&Peer{
			ID:     node1.ID,
			PubKey: node1.pub,
			Host:   node1.host,
		}, peer)
	})

	t.Run("ask for known unreachable", func(t *testing.T) {
		assert := assert.New(t)

		known := FakePeer("unreachable")
		store1.SetPeer(known)

		node2.AskPeerInfo(node1.ID, node1.host, &known.ID)

		peer := store2.GetPeer(known.ID)
		assert.Equal(known, peer)
	})

	t.Run("ask for known invalid", func(t *testing.T) {
		assert := assert.New(t)

		known := InvalidPeer("invalid")
		store1.SetPeer(known)

		node2.AskPeerInfo(node1.ID, node1.host, &known.ID)

		peer := store2.GetPeer(known.ID)
		assert.Nil(peer)
	})
}

/*
 * Utils:
 */

// FakePeer returns fake peer info.
func FakePeer(host string) *Peer {
	key, err := crypto.GenerateECDSAKey()
	if err != nil {
		panic(err)
	}

	return &Peer{
		ID:     CalcPeerID(&key.PublicKey),
		PubKey: &key.PublicKey,
		Host:   host,
	}
}

// InvalidPeer returns invalid peer info.
func InvalidPeer(host string) *Peer {
	peer := FakePeer(host)
	peer.ID = hash.FakePeer()
	return peer
}
