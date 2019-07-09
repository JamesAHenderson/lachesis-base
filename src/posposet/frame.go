package posposet

import (
	"sync/atomic"

	"github.com/Fantom-foundation/go-lachesis/src/hash"
	"github.com/Fantom-foundation/go-lachesis/src/inter/idx"
	"github.com/Fantom-foundation/go-lachesis/src/posposet/wire"
)

// TODO: make Frame internal

// Frame is a consensus tables for frame.
type Frame struct {
	Index     idx.Frame
	FlagTable FlagTable

	Balances hash.Hash // TODO: move to super-frame

	save func()
}

// Save calls .save() if set.
func (f *Frame) Save() {
	if f.save != nil {
		f.save()
	}
}

// AddRootsOf appends known roots for event.
func (f *Frame) AddRootsOf(event hash.Event, roots EventsByPeer) {
	if f.FlagTable[event] == nil {
		f.FlagTable[event] = EventsByPeer{}
	}
	if f.FlagTable[event].Add(roots) {
		f.Save()
	}
}

// GetRootsOf returns known roots of event. For read only, please.
func (f *Frame) GetRootsOf(event hash.Event) EventsByPeer {
	return f.FlagTable[event]
}

// SetBalances saves PoS-balances state.
func (f *Frame) SetBalances(balances hash.Hash) bool {
	if f.Balances != balances {
		f.Balances = balances
		f.Save()
		return true
	}
	return false
}

// ToWire converts to proto.Message.
func (f *Frame) ToWire() *wire.Frame {
	return &wire.Frame{
		Index:     uint32(f.Index),
		FlagTable: f.FlagTable.ToWire(),
		Balances:  f.Balances.Bytes(),
	}
}

// WireToFrame converts from wire.
func WireToFrame(w *wire.Frame) *Frame {
	if w == nil {
		return nil
	}
	return &Frame{
		Index:     idx.Frame(w.Index),
		FlagTable: WireToFlagTable(w.FlagTable),
		Balances:  hash.FromBytes(w.Balances),
	}
}

/*
 * Poset's methods:
 */

func (p *Poset) setFrameSaving(f *Frame) {
	f.save = func() {
		if f.Index > p.LastFinishedFrameN() {
			p.store.SetFrame(f, p.SuperFrameN)
		} else {
			p.Fatalf("frame %d is finished and should not be changed", f.Index)
		}
	}
}

// LastSuperFrame returns super-frame and list of peers
func (p *Poset) LastSuperFrame() (idx.SuperFrame, []hash.Peer) {
	n := idx.SuperFrame(atomic.LoadUint64((*uint64)(&p.SuperFrameN)))

	return n, p.SuperFrame(n)
}

// SuperFrame returns list of peers for n super-frame
func (p *Poset) SuperFrame(n idx.SuperFrame) []hash.Peer {
	members := p.store.GetMembers(n)

	addrs := make([]hash.Peer, 0, len(members))

	for member := range members {
		addrs = append(addrs, member)
	}

	return addrs
}
