// This file implements FCIndexer (forkless-cause indexer) which is used as a part of both
// - choosing parents for a new event, and
// - deciding when to emit a new event.
// The methods implement two related metrics of DAG progress that measure the knowledge of roots in an event's subgraph. Within an event's subgraph root knowledge may be characterised
// by an nxn binary matrix K with the ijth entry of K indicating whether the subgraph of validator i's events contain a root of validator j.
// (i)	rootProgress: A metric that measures the progress of a validator in producing its next root event, measuring how close the event is to becoming forkless-caused by quorum validators.
//		The metric sums over entries (to a maximum of quorum) for the quorum roots closest to satisfying forkless-cause, using the largest staked root(s) when roots have equal (including zero) progress.
// (ii) rootKnowledge: A metric that measures (within an event's subgraph) which validators subgraph's contain which validator's roots, i.e. the metric sums over all elements of K.
//
// Parent selection uses (i) because it is a direct measure of progress toward producing a new root, while emission timing uses (ii) because it is always possible to increase
// the metric provided that quourm validators are producing new DAG progressing events.

package ancestor

import (
	"sort"

	"github.com/Fantom-foundation/lachesis-base/hash"
	"github.com/Fantom-foundation/lachesis-base/inter/dag"
	"github.com/Fantom-foundation/lachesis-base/inter/idx"
	"github.com/Fantom-foundation/lachesis-base/inter/pos"
	"github.com/Fantom-foundation/lachesis-base/utils/piecefunc"
)

const (
	MaxFramesToIndex = 500
)

type sortedRootKnowledge []RootKnowledge

type RootKnowledge struct {
	k     Metric
	stake pos.Weight
	root  dag.Event
}

type highestEvent struct {
	id            hash.Event
	frame         idx.Frame
	rootKnowledge Metric
}

type FCIndexer struct {
	dagi       DagIndex
	validators *pos.Validators
	me         idx.ValidatorID

	TopFrame idx.Frame

	FrameRoots map[idx.Frame]dag.Events

	highestEvents map[idx.ValidatorID]highestEvent

	searchStrategy SearchStrategy
}

type DagIndex interface {
	ForklessCauseProgress(aID, bID hash.Event, candidateParents, chosenParents hash.Events) (*pos.WeightCounter, []*pos.WeightCounter)
}

func NewFCIndexer(validators *pos.Validators, dagi DagIndex, me idx.ValidatorID) *FCIndexer {
	fc := &FCIndexer{
		dagi:          dagi,
		validators:    validators,
		me:            me,
		FrameRoots:    make(map[idx.Frame]dag.Events),
		highestEvents: make(map[idx.ValidatorID]highestEvent),
	}
	fc.searchStrategy = NewMetricStrategy(fc.GetMetricOf)
	return fc
}

// ProcessEvent should be called every time a new event is added to the DAG. It is used to collect information within an FCIndexer that is required for computing the rootProgress and rootKnowledge metrics
func (fc *FCIndexer) ProcessEvent(e dag.Event, eSelfParent dag.Event) {
	eRootKnowledge := fc.rootKnowledge(e.Frame(), e.ID(), nil)
	existingHighestEvent := fc.highestEvents[e.Creator()]
	if fc.greaterEqual(eRootKnowledge, e.Frame(), existingHighestEvent.rootKnowledge, existingHighestEvent.frame) { // compare metrics of existing and new events to ensure highestBefore always has largest metric of any fork

		fc.highestEvents[e.Creator()] = highestEvent{
			id:            e.ID(),
			frame:         e.Frame(),
			rootKnowledge: eRootKnowledge,
		}

		if fc.TopFrame < e.Frame() {
			fc.TopFrame = e.Frame()
			// frames should get incremented by one, so gaps shouldn't be possible
			delete(fc.FrameRoots, fc.TopFrame-MaxFramesToIndex)
		}
		f := e.Frame()

		if e.SelfParent() == nil { // this means e is a leaf event which is also a root
			frameRoots := fc.FrameRoots[f]
			if frameRoots == nil {
				frameRoots = make(dag.Events, fc.validators.Len())
			}
			frameRoots[fc.validators.GetIdx(e.Creator())] = e
			fc.FrameRoots[f] = frameRoots
		} else {
			if f > eSelfParent.Frame() { // Incase of fork-events, it is necessary to compare to the frame of e.SelfParent(), not existingHighestBefore which may be from a different fork. If the frame of the new event is larger than the frame of its self parent, then the new event must be a root
				frameRoots := fc.FrameRoots[f]
				if frameRoots == nil {
					frameRoots = make(dag.Events, fc.validators.Len())
				}
				frameRoots[fc.validators.GetIdx(e.Creator())] = e
				fc.FrameRoots[f] = frameRoots
			}
		}
	}
}

// rootKnowledge computes the knowledge of roots amongst validators by counting which validators known which roots.
// Root knowledge is an nxn binary matrix K. The ijth entry of the matrix is 1 if a root of validator i is in the
// subgraph of events created by by validator j, all within the subgraph of event, and zero otherwise.
// The function returns a metric counting the number of non-zero entries of the root knowledge matrix K.
func (fc *FCIndexer) rootKnowledge(frame idx.Frame, event hash.Event, chosenHeads hash.Events) Metric {
	roots, ok := fc.FrameRoots[frame]
	if !ok {
		return 0
	}
	var numNonZero Metric = 0 // number of non-zero entries in the root knowledge matrix
	for _, root := range roots {
		if root == nil {
			continue
		}
		FCProgress, _ := fc.dagi.ForklessCauseProgress(event, root.ID(), nil, chosenHeads)
		numNonZero += Metric(FCProgress.NumCounted()) // add the number of validators that have observed root
	}
	return numNonZero
}

// Check if root knowledge will increase when using heads as parents for a new event
func (fc *FCIndexer) RootKnowledgeIncrease(heads hash.Events) bool {

	prevK := fc.highestEvents[fc.me].rootKnowledge
	newK := fc.rootKnowledge(fc.highestEvents[fc.me].frame, fc.highestEvents[fc.me].id, heads)

	return newK > prevK
}

func (fc *FCIndexer) greaterEqual(aK Metric, aFrame idx.Frame, bK Metric, bFrame idx.Frame) bool {
	if aFrame != bFrame {
		return aFrame > bFrame
	}
	return aK >= bK
}

// ValidatorsPastMe returns total weight of validators which exceeded root knowledge of "my" previous event
// Typically node shouldn't emit an event until the value >= quorum, which happens to lead to an almost optimal events timing
func (fc *FCIndexer) ValidatorsPastMe() pos.Weight {
	kGreaterWeight := fc.validators.NewCounter()

	kSelf := fc.highestEvents[fc.me].rootKnowledge
	selfFrame := fc.highestEvents[fc.me].frame
	for creator, e := range fc.highestEvents {
		if fc.greaterEqual(e.rootKnowledge, e.frame, kSelf, selfFrame) {
			kGreaterWeight.Count(creator)
		}
	}
	return kGreaterWeight.Sum()
}

func (fc *FCIndexer) GetMetricOf(ids hash.Events) Metric {
	if fc.TopFrame == 0 {
		return 0
	}
	return Metric(fc.rootProgress(fc.TopFrame, ids[0], ids[1:]))
	// return Metric(fc.rootKnowledge(fc.TopFrame, ids[0], ids[1:]))
}

func (fc *FCIndexer) SearchStrategy() SearchStrategy {
	return fc.searchStrategy
}

// rootProgress computes the progress of a validator toward producing a new root
// This progress can be conceptualised via a binary matrix indexed by roots and validators
// The ijth entry of the matrix is 1 if root i is known by validator j in the subgraph of event, and zero otherwise
// A new root is created when quorum roots are each known by quorum validators in the subgraph of event
// (note that roots can be known by different sets of validators)
// This is a count based metric. Roots are sorted according to those that are closest to being known by quorum stake, with roots known
// by equal stake ordered according to the stake of the root's creator
// From these sorted roots the quorum most well known roots are taken
// For each of these sorted roots, the number of validators that know each root are counted.
// The minimum number of extra validator validators required to reach quourum are also counted.
// (i.e. the largest validators that do not yet know the root). This will be zero if the root is already known by quorum.
// The root progress metric is computed by dividing the number of validators that know the root by
// the number of validators that know the root plus the minimum number of validators to reach quorum.
// The metric is in the range [0,1].
func (fc *FCIndexer) rootProgress(frame idx.Frame, event hash.Event, chosenHeads hash.Events) Metric {
	roots := fc.FrameRoots[frame]

	sortedWeights := fc.validators.SortedWeights()
	sortedIDs := fc.validators.SortedIDs()

	// find k_root, the number of validators that know each root, divided by the minimum number of validators for quorum
	// up to a maximum of 1.0 (being known by more than quorum doesn't increase beyond 1.0)
	RootKnowledge := make([]RootKnowledge, len(roots))
	for i, root := range roots {
		if root != nil {
			RootKnowledge[i].root = root // record the root
			rootStake := fc.validators.Get(root.Creator())
			RootKnowledge[i].stake = rootStake // record the stake of the root

			FCProgress, _ := fc.dagi.ForklessCauseProgress(event, root.ID(), nil, chosenHeads) //compute ForklessCauseProgress to find which validators know root in event's subgraph
			if FCProgress.HasQuorum() {
				RootKnowledge[i].k = piecefunc.DecimalUnit //k_root has maximum value of 1 when root is known by at least a quorum
			} else {
				// root is known by less than a quorum
				numCounted := Metric(FCProgress.NumCounted()) //the number of nodes that know root (the numerator of k_root)
				// now find the denominator of k_root; the minimum number of additional nodes needed for quorum (if any)
				numForQ := Metric(FCProgress.NumCounted())
				stake := FCProgress.Sum()
				for j, weight := range sortedWeights {
					if stake >= fc.validators.Quorum() {
						break
					}
					if FCProgress.Count(sortedIDs[j]) {
						stake += weight
						numForQ++
					}
				}
				RootKnowledge[i].k = (numCounted * piecefunc.DecimalUnit) / numForQ
			}
		}
	}

	//sort roots by k value to ge the most known roots by count
	sort.Sort(sortedRootKnowledge(RootKnowledge))
	var kNew Metric = 0

	// now find combined knowledge of quorum best known roots
	// sum k_root for the best known roots, to get the numerator
	var bestRootsStake pos.Weight = 0            // used to count stake of the best known roots
	rootValidators := make([]idx.ValidatorID, 0) // used to record which validators have had their root counted
	var numRootsForQ Metric = 0
	for _, rootK := range RootKnowledge {
		if rootK.root != nil {
			rootStake := fc.validators.Get(rootK.root.Creator())
			if bestRootsStake >= fc.validators.Quorum() {
				break
			} else if bestRootsStake+rootStake <= fc.validators.Quorum() {
				kNew += rootK.k
				bestRootsStake += rootStake
				numRootsForQ++
				rootValidators = append(rootValidators, rootK.root.Creator())
			} else {
				kNew += rootK.k
				bestRootsStake = fc.validators.Quorum() // this will trigger the break condition above
				numRootsForQ++
				rootValidators = append(rootValidators, rootK.root.Creator())
			}
		}
	}

	// it may be that less than quorum roots have been observed
	// to get the denominator calculate how many extra roots are needed for quorum (if any),
	// starting from the largest validator
	for i, weight := range sortedWeights {
		if bestRootsStake >= fc.validators.Quorum() {
			break
		}
		// check if the validator has already been counted as one of the best known roots
		notCounted := true
		for _, rootValidator := range rootValidators {
			if sortedIDs[i] == rootValidator {
				notCounted = false
				break
			}
		}
		if notCounted {
			bestRootsStake += weight
			numRootsForQ++
		}
	}
	return kNew / numRootsForQ // this result should be less than or equal to 1
}
func (m sortedRootKnowledge) Len() int {
	return len(m)
}

func (m sortedRootKnowledge) Swap(i, j int) {
	m[i], m[j] = m[j], m[i]
}

func (m sortedRootKnowledge) Less(i, j int) bool {
	if m[i].k != m[j].k {
		return m[i].k > m[j].k
	} else {
		return m[i].stake > m[j].stake
	}
}
