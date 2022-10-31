package ancestor

import (
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/Fantom-foundation/lachesis-base/abft/dagidx"
	"github.com/Fantom-foundation/lachesis-base/hash"
	"github.com/Fantom-foundation/lachesis-base/inter/dag"
	"github.com/Fantom-foundation/lachesis-base/inter/idx"
	"github.com/Fantom-foundation/lachesis-base/inter/pos"
	"github.com/Fantom-foundation/lachesis-base/utils/wmedian"
)

type DagIndex interface {
	dagidx.VectorClock
}
type DiffMetricFn func(median, current, update idx.Event, validatorIdx idx.Validator) Metric

type QuorumIndexer struct {
	dagi       DagIndex
	validators *pos.Validators

	globalMatrix     Matrix
	selfParentSeqs   []idx.Event
	globalMedianSeqs []idx.Event
	dirty            bool
	searchStrategy   SearchStrategy

	diffMetricFn DiffMetricFn

	randParent      *rand.Rand
	SelfParentEvent hash.Event
}

func NewQuorumIndexer(validators *pos.Validators, dagi DagIndex, diffMetricFn DiffMetricFn) *QuorumIndexer {
	return &QuorumIndexer{
		globalMatrix:     NewMatrix(validators.Len(), validators.Len()),
		globalMedianSeqs: make([]idx.Event, validators.Len()),
		selfParentSeqs:   make([]idx.Event, validators.Len()),
		dagi:             dagi,
		validators:       validators,
		diffMetricFn:     diffMetricFn,
		dirty:            true,
		randParent:       rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

type Matrix struct {
	buffer  []idx.Event
	columns idx.Validator
}

func NewMatrix(rows, cols idx.Validator) Matrix {
	return Matrix{
		buffer:  make([]idx.Event, rows*cols),
		columns: cols,
	}
}

func (m Matrix) Row(i idx.Validator) []idx.Event {
	return m.buffer[i*m.columns : (i+1)*m.columns]
}

func (m Matrix) Clone() Matrix {
	buffer := make([]idx.Event, len(m.buffer))
	copy(buffer, m.buffer)
	return Matrix{
		buffer,
		m.columns,
	}
}

func seqOf(seq dagidx.Seq) idx.Event {
	if seq.IsForkDetected() {
		return math.MaxUint32/2 - 1
	}
	return seq.Seq()
}

type weightedSeq struct {
	seq    idx.Event
	weight pos.Weight
}

func (ws weightedSeq) Weight() pos.Weight {
	return ws.weight
}

func (h *QuorumIndexer) ProcessEvent(event dag.Event, selfEvent bool) {
	vecClock := h.dagi.GetMergedHighestBefore(event.ID())
	creatorIdx := h.validators.GetIdx(event.Creator())
	// update global matrix
	for validatorIdx := idx.Validator(0); validatorIdx < h.validators.Len(); validatorIdx++ {
		seq := seqOf(vecClock.Get(validatorIdx))
		h.globalMatrix.Row(validatorIdx)[creatorIdx] = seq
		if selfEvent {
			h.selfParentSeqs[validatorIdx] = seq
		}
	}
	h.dirty = true
}

func (h *QuorumIndexer) recacheState() {
	// update median seqs
	for validatorIdx := idx.Validator(0); validatorIdx < h.validators.Len(); validatorIdx++ {
		pairs := make([]wmedian.WeightedValue, h.validators.Len())
		for i := range pairs {
			pairs[i] = weightedSeq{
				seq:    h.globalMatrix.Row(validatorIdx)[i],
				weight: h.validators.GetWeightByIdx(idx.Validator(i)),
			}
		}
		sort.Slice(pairs, func(i, j int) bool {
			a, b := pairs[i].(weightedSeq), pairs[j].(weightedSeq)
			return a.seq > b.seq
		})
		median := wmedian.Of(pairs, h.validators.Quorum())
		h.globalMedianSeqs[validatorIdx] = median.(weightedSeq).seq
	}
	// invalidate search strategy cache
	cache := NewMetricFnCache(h.GetMetricOf, 128)
	h.searchStrategy = NewMetricStrategy(cache.GetMetricOf)
	h.dirty = false
}

func (h *QuorumIndexer) GetMetricOf(id hash.Event) Metric {
	if h.dirty {
		h.recacheState()
	}
	vecClock := h.dagi.GetMergedHighestBefore(id)
	var metric Metric
	for validatorIdx := idx.Validator(0); validatorIdx < h.validators.Len(); validatorIdx++ {
		update := seqOf(vecClock.Get(validatorIdx))
		current := h.selfParentSeqs[validatorIdx]
		median := h.globalMedianSeqs[validatorIdx]
		metric += h.diffMetricFn(median, current, update, validatorIdx)
	}
	return metric
}

func (h *QuorumIndexer) SearchStrategy() SearchStrategy {
	if h.dirty {
		h.recacheState()
	}
	return h.searchStrategy
}

func (h *QuorumIndexer) GetGlobalMedianSeqs() []idx.Event {
	if h.dirty {
		h.recacheState()
	}
	return h.globalMedianSeqs
}

func (h *QuorumIndexer) GetGlobalMatrix() Matrix {
	return h.globalMatrix
}

func (h *QuorumIndexer) GetSelfParentSeqs() []idx.Event {
	return h.selfParentSeqs
}

func (h *QuorumIndexer) Choose(chosenParents hash.Events, candidateParents hash.Events) int {
	metrics := make([]float64, len(candidateParents))
	// first get metrics of each options
	metrics = h.GetMetricsOfRootProgress(candidateParents, chosenParents) //should call GetMetricsofRootProgress
	if metrics == nil {
		// this occurs if all head options are at a previous frame, and thus cannot progress the production of a root in the current frame
		// in this case return a random head
		return h.randParent.Intn(len(candidateParents))
	}
	// now sort options based on metrics in order of importance
	sort.Sort(sortedRootProgressMetrics(metrics))

	return metrics[0].idx
}

func (h *QuorumIndexer) GetMetricsOfRootProgress(candidateParents hash.Events, chosenParents hash.Events) []RootProgressMetrics {
	// This function is indended to be used in the process of
	// selecting event parents from a set of candidate parents.
	// This function returns a metric of root knowledge for assessing
	// the incremental progress when using each candidate head as a parent
	// chosenParents are parents that have already been selected

	// find the maximum frame number of all parents
	maxFrame := h.dagi.GetEvent(h.SelfParentEvent).Frame()
	candidateParentFrame := make([]idx.Frame, len(candidateParents))

	for i, head := range candidateParents {
		candidateParentFrame[i] = h.dagi.GetEvent(head).Frame()
		if candidateParentFrame[i] > maxFrame {
			maxFrame = candidateParentFrame[i]
		}
	}

	for _, parent := range chosenParents {
		if h.dagi.GetEvent(parent).Frame() > maxFrame {
			maxFrame = h.dagi.GetEvent(parent).Frame()
		}
	}

	// only retain candidateParents that have reached maxFrame
	// (parents that haven't reached maxFrame cannot provide extra progress)
	var maxFrameParents hash.Events
	for i, parent := range candidateParents {
		if candidateParentFrame[i] >= maxFrame {
			// rootProgressMetrics = append(rootProgressMetrics, h.newRootProgressMetrics(i))
			maxFrameParents = append(maxFrameParents, parent)
		}
	}
	rootProgressMetrics := make([]float64, len(maxFrameParents)) // create a slice of metrics for each candidate parent that has reached maxFrame

	maxFrameRoots := h.lachesis.Store.GetFrameRoots(maxFrame)
	for i, _ := range maxFrameParents {
		candidateParents := make([]hash.Event, len(chosenParents)+1)
		for j, head := range chosenParents {
			candidateParents[j] = head
		}
		candidateParents[len(candidateParents)-1] = maxFrameParents[i]
		rootProgressMetrics[i] = h.newRootProgress(maxFrame, h.SelfParentEvent, candidateParents) //best
	}
	return rootProgressMetrics
}

func (h *QuorumIndexer) newRootProgress(frame idx.Frame, event hash.Event, chosenHeads hash.Events) float64 {
	// This function computes the progress of a validator toward producing a new root
	// This progress can be conceptualised via a binary matrix indexed by roots and validators
	// The ijth entry of the matrix is 1 if root i is known by validator j in the subgraph of event, and zero otherwise
	// A new root is created when quorum roots are each known by quorum validators in the subgraph of event
	// (note that roots can be known by different sets of validators)
	// Roots are sorted according to those that are closest to being known by quorum stake, with roots known
	// by equal stake ordered according to the stake of the root's creator
	// From these sorted roots the quorum most well known roots are taken
	// For each of these sorted roots, the number of validators that know each root are counted.
	// The minimum number of extra validator validators required to reach quourum are also counted.
	// (i.e. the largest validators that do not yet know the root). This will be zero if the root is already known by quorum.
	// The root progress metric is computed by dividing the number of validators that know the root by
	// the number of validators that know the root plus the minimum number of validators to reach quorum.
	// The metric is in the range [0,1].

	roots := h.lachesis.Store.GetFrameRoots(frame)

	weights := h.validators.SortedWeights()
	ids := h.validators.SortedIDs()

	//calculate k_i for each root i
	RootKnowledge := make([]KIdx, len(roots))
	for i, root := range roots {
		FCProgress := h.lachesis.DagIndex.ForklessCauseProgress(event, root.ID, nil, chosenHeads) //compute for new event
		if FCProgress[0].HasQuorum() {
			RootKnowledge[i].K = 1.0 //k_i has maximum value of 1 when root i is known by at least a quorum
		} else {
			// root i is known by less than a quorum
			numCounted := FCProgress[0].NumCounted() //the number of nodes that know the root (the numerator of k_i)
			// now find the denominator of k_i; the number of additional nodes needed to for quorum (if any)
			numForQ := FCProgress[0].NumCounted()
			stake := FCProgress[0].Sum()
			for j, weight := range weights {
				if stake >= h.validators.Quorum() {
					break
				}
				if FCProgress[0].Count(ids[j]) {
					stake += weight
					numForQ++
				}
			}
			RootKnowledge[i].K = float64(numCounted) / float64(numForQ)
		}
		RootKnowledge[i].Root = root // record which root the k_i is for
	}

	//sort roots by k_i value to ge the best roots
	sort.Sort(sortedKIdx(RootKnowledge))
	var kNew float64 = 0

	// sum k_i for the best known roots, to get the numerator of k
	var bestRootsStake pos.Weight = 0
	rootValidators := make([]idx.Validator, 0)
	numRootsForQ := 0.0
	for _, kidx := range RootKnowledge {
		rootValidatorIdx := h.validators.GetIdx(kidx.Root.Slot.Validator)
		rootStake := h.validators.GetWeightByIdx(rootValidatorIdx)
		if bestRootsStake >= h.validators.Quorum() {
			break
		} else if bestRootsStake+rootStake <= h.validators.Quorum() {
			kNew += kidx.K
			bestRootsStake += rootStake
			numRootsForQ++
			rootValidators = append(rootValidators, rootValidatorIdx)
		} else {
			kNew += kidx.K
			bestRootsStake = h.validators.Quorum() // this will trigger the break condition above
			numRootsForQ++
			rootValidators = append(rootValidators, rootValidatorIdx)
		}
	}

	// calculate how many extra roots are needed for quorum (if any), to get the denominator of k
	for i, weight := range weights {
		if bestRootsStake >= h.validators.Quorum() {
			break
		}
		notCounted := true
		for _, rootValidator := range rootValidators {
			if ids[i] == idx.ValidatorID(rootValidator) {
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
