package ancestor

import (
	"math/rand"
	"sort"

	"github.com/Fantom-foundation/lachesis-base/abft"
	"github.com/Fantom-foundation/lachesis-base/abft/dagidx"
	"github.com/Fantom-foundation/lachesis-base/abft/election"
	"github.com/Fantom-foundation/lachesis-base/hash"
	"github.com/Fantom-foundation/lachesis-base/inter/dag"
	"github.com/Fantom-foundation/lachesis-base/inter/idx"
	"github.com/Fantom-foundation/lachesis-base/inter/pos"
)

type sortedRootKnowledge []RootKnowledge

type RootKnowledge struct {
	k     float64
	stake pos.Weight
	root  election.RootAndSlot
}

type sortedRootProgressMetrics []RootProgressMetrics

type RootProgressMetrics struct {
	idx   int
	stake pos.Weight
	k     float64
}

type validatorHighestEvent struct {
	event   dag.Event
	time    int //+++TODO CHANGE TO APPROPRIATE TIME DATATYPE
	kChange bool
	k       int
}

type Store interface {
	GetFrameRoots(f idx.Frame) []election.RootAndSlot
}

type DagIndex interface {
	dagidx.VectorClock
	dagidx.ForklessCause
	GetEvent(hash.Event) dag.Event
}
type DiffMetricFn func(median, current, update idx.Event, validatorIdx idx.Validator) Metric

type QuorumIndexer struct {
	dagi       DagIndex
	store      Store
	validators *pos.Validators

	randParent      *rand.Rand
	SelfParentEvent hash.Event

	validatorHighestEvents []validatorHighestEvent
}

func NewQuorumIndexer(validators *pos.Validators, lchs *abft.TestLachesis) *QuorumIndexer {
	return &QuorumIndexer{
		dagi:       lchs.DagIndex,
		store:      lchs.Store,
		validators: validators,

		randParent:             rand.New(rand.NewSource(0)), // +++TODO rand.New(rand.NewSource(time.Now().UnixNano())),
		validatorHighestEvents: make([]validatorHighestEvent, validators.Len()),
	}
}

func (h *QuorumIndexer) ProcessEvent(event dag.Event, selfEvent bool, time int) {
	// This function should be called each time a new event is added to the DAG.
	// This function records quantities that are needed for event timing
	creatorIdx := h.validators.GetIdx(event.Creator())
	if h.validatorHighestEvents[creatorIdx].event != nil {
		if event.Seq() > h.validatorHighestEvents[creatorIdx].event.Seq() { // check event occurs after existing event
			kNew := h.RootKnowledgeByCount(event.Frame(), event.ID(), nil)
			kPrev := h.validatorHighestEvents[creatorIdx].k
			h.validatorHighestEvents[creatorIdx].kChange = (kNew != kPrev)
			// store values for the new event
			h.validatorHighestEvents[creatorIdx].k = kNew
			h.validatorHighestEvents[creatorIdx].event = event
			h.validatorHighestEvents[creatorIdx].time = time

		}
	} else {
		kNew := h.RootKnowledgeByCount(event.Frame(), event.ID(), nil) // calculate the DAG progress of the new event
		kPrev := 0                                                     // no previous event is known
		h.validatorHighestEvents[creatorIdx].kChange = (kNew != kPrev)
		// store values for the new event
		h.validatorHighestEvents[creatorIdx].k = kNew
		h.validatorHighestEvents[creatorIdx].event = event
		h.validatorHighestEvents[creatorIdx].time = time
	}
}

func (h *QuorumIndexer) Choose(chosenParents hash.Events, candidateParents hash.Events) int {
	metrics := h.GetMetricsOfRootProgress(candidateParents, chosenParents) // metric for each candidate parent
	if metrics == nil {
		// this occurs if all candidate parents are at a previous frame, and thus cannot progress the production of a root in the current frame
		// in this case return a random candidate parent
		return h.randParent.Intn(len(candidateParents))
	}
	// now sort options based on metrics in order of importance
	sort.Sort(sortedRootProgressMetrics(metrics))

	return metrics[0].idx
}

func (h *QuorumIndexer) GetMetricsOfRootProgress(candidateParents hash.Events, chosenParents hash.Events) []RootProgressMetrics {
	// This function is indended to be used in the process of
	// selecting parents from a set of candidate parents.
	// This function returns a metric of root knowledge for assessing
	// the incremental progress when using each candidate head as a parent.
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

	var rootProgressMetrics []RootProgressMetrics // create a slice of metrics for each candidate parent that has reached maxFrame

	// only retain candidateParents that have reached maxFrame
	// (parents that haven't reached maxFrame cannot provide extra progress)
	var maxFrameParents hash.Events
	var tempMetric RootProgressMetrics
	tempMetric.k = 0
	for i, parent := range candidateParents {
		if candidateParentFrame[i] >= maxFrame {
			tempMetric.idx = i
			rootProgressMetrics = append(rootProgressMetrics, tempMetric)
			maxFrameParents = append(maxFrameParents, parent)
		}
	}
	// create a slice containing all chosen parents, and a candidate parent
	parents := make([]hash.Event, len(chosenParents)+1)
	for j, head := range chosenParents {
		parents[j] = head
	}
	for i, candidateParent := range maxFrameParents {
		parents[len(parents)-1] = candidateParent
		rootProgressMetrics[i].k = h.progressTowardNewRoot(maxFrame, h.SelfParentEvent, parents)

		candidateParentIdx := h.validators.GetIdx(h.dagi.GetEvent(candidateParent).Creator())
		rootProgressMetrics[i].stake = h.validators.GetWeightByIdx(candidateParentIdx)

	}

	return rootProgressMetrics
}

func (h *QuorumIndexer) progressTowardNewRoot(frame idx.Frame, event hash.Event, chosenHeads hash.Events) float64 {
	// This function computes the progress of a validator toward producing a new root
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

	roots := h.store.GetFrameRoots(frame)

	sortedWeights := h.validators.SortedWeights()
	sortedIDs := h.validators.SortedIDs()

	// find k_root, the number of validators that know each root, divided by the minimum number of validators for quorum
	// up to a maximum of 1.0 (being known by more than quorum doesn't increase )
	RootKnowledge := make([]RootKnowledge, len(roots))
	for i, root := range roots {
		RootKnowledge[i].root = root // record the root
		rootValidatorIdx := h.validators.GetIdx(root.Slot.Validator)
		rootStake := h.validators.GetWeightByIdx(rootValidatorIdx)
		RootKnowledge[i].stake = rootStake // record the stake of the root

		FCProgress, _ := h.dagi.ForklessCauseProgress(event, root.ID, nil, chosenHeads) //compute ForklessCauseProgress to find which validators know root in event's subgraph
		if FCProgress.HasQuorum() {
			RootKnowledge[i].k = 1.0 //k_root has maximum value of 1 when root is known by at least a quorum
		} else {
			// root is known by less than a quorum
			numCounted := FCProgress.NumCounted() //the number of nodes that know root (the numerator of k_root)
			// now find the denominator of k_root; the minimum number of additional nodes needed for quorum (if any)
			numForQ := FCProgress.NumCounted()
			stake := FCProgress.Sum()
			for j, weight := range sortedWeights {
				if stake >= h.validators.Quorum() {
					break
				}
				if FCProgress.Count(sortedIDs[j]) {
					stake += weight
					numForQ++
				}
			}
			RootKnowledge[i].k = float64(numCounted) / float64(numForQ)
		}

	}

	//sort roots by k value to ge the most known roots by count
	sort.Sort(sortedRootKnowledge(RootKnowledge))
	var kNew float64 = 0

	// now find combined knowledge of quorum best known roots
	// sum k_root for the best known roots, to get the numerator
	var bestRootsStake pos.Weight = 0            // used to count stake of the best known roots
	rootValidators := make([]idx.ValidatorID, 0) // used to record which validators have had their root counted
	numRootsForQ := 0.0
	for _, rootK := range RootKnowledge {
		rootValidatorIdx := h.validators.GetIdx(rootK.root.Slot.Validator)
		rootStake := h.validators.GetWeightByIdx(rootValidatorIdx)
		if bestRootsStake >= h.validators.Quorum() {
			break
		} else if bestRootsStake+rootStake <= h.validators.Quorum() {
			kNew += rootK.k
			bestRootsStake += rootStake
			numRootsForQ++
			rootValidators = append(rootValidators, rootK.root.Slot.Validator)
		} else {
			kNew += rootK.k
			bestRootsStake = h.validators.Quorum() // this will trigger the break condition above
			numRootsForQ++
			rootValidators = append(rootValidators, rootK.root.Slot.Validator)
		}
	}

	// it may be that less than quorum roots have not been created yet
	// to get the denominator calculate how many extra roots are needed for quorum (if any),
	// starting from the largest validator
	for i, weight := range sortedWeights {
		if bestRootsStake >= h.validators.Quorum() {
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

func (m sortedRootProgressMetrics) Len() int {
	return len(m)
}

func (m sortedRootProgressMetrics) Swap(i, j int) {
	m[i], m[j] = m[j], m[i]
}

func (m sortedRootProgressMetrics) Less(i, j int) bool {
	if m[i].k != m[j].k {
		return m[i].k > m[j].k
	} else {
		return m[i].stake > m[j].stake
	}
}

func (h *QuorumIndexer) RootKnowledgeByCount(frame idx.Frame, event hash.Event, chosenHeads hash.Events) int {
	// This function computes the knowledge of roots amongst validators by counting which validators known which roots.
	// Root knowledge is a binary matrix indexed by roots and validators.
	// The ijth entry of the matrix is 1 if root i is known by validator j in the subgraph of event, and zero otherwise.
	// The function returns a metric counting the number of non-zero entries of the root knowledge matrix.
	roots := h.store.GetFrameRoots(frame)
	numNonZero := 0 // number of non-zero entries in the root knowledge matrix
	for _, root := range roots {
		FCProgress, _ := h.dagi.ForklessCauseProgress(event, root.ID, nil, chosenHeads)
		numNonZero += FCProgress.NumCounted() // add the number of validators that have observed root
	}
	return numNonZero
}

func (h *QuorumIndexer) Threshold() uint32 {
	// This function computes the threshold to be used by DAGProgressEventTimingCondition.
	// The thresholds are chosen in a way that ensures that the event timing method in DAGProgressEventTimingCondition
	// will not cause event production to stall, provided that quorum validators continue producing events that progress
	// the DAG toward completing frames.
	// This is done by iteratively finding the smallest number of validators required for quorum as validators go offline, and using that number as
	// the threshold for those validators in that quorum. Initially all validators are assumed to be online and the largest validators, after each
	// iteration the smallest validator of the previous quorum is added to a set of offline validators, so that additional smaller validators
	// are required to join the quorum. This simulates the effect validators .The threshold of those newly joining validators is set to the number required to achieve quorum.
	// The end result is that the larger the stake of a validator, the smaller its threshold and vice versa. This means that large validators are biased toward producing events
	// more rapidly than smaller validators. However, this is usually not a signficant bias relative to the ratio of stake between small and large validators. Further control of a validator's event rate should be managed according to its available gas,
	// using these thresholds the network will not stall provided quorum are progressing the DAG

	selfID := h.dagi.GetEvent(h.SelfParentEvent).Creator()
	selfStake := h.validators.GetWeightByIdx(h.validators.GetIdx(selfID))
	sortedWeights := h.validators.SortedWeights()
	sortedIDs := h.validators.SortedIDs()

	var threshold uint32 = 0

	offline := make([]bool, h.validators.Len()) //used to indicate scenario of some nodes offline
	selfCounted := false                        // used to indicate if self has been counted
	firstCount := true
	lastCounted := 0
	// +++TODO this doesn't account for multiple equal stakes
	for {
		// first find minimum number of largest, online validators required to acheive quorum
		quorumCounter := h.validators.NewCounter()
		var NumQuorum uint32 = 0
		for i, ID := range sortedIDs {
			if ID == selfID {
				selfCounted = true // self is included in quorum
			}
			if !offline[i] {
				quorumCounter.Count(ID)
				lastCounted = i //record the smallest counted validator, to be set offline later
				NumQuorum++     // count the number of online validators required for quorum
			}
			if quorumCounter.HasQuorum() {
				// Validators that have equal stake have equal threshold (no ordering/preference is assumed amongst equal validators), check if self has equal stake to last counted
				if selfStake == sortedWeights[lastCounted] {
					selfCounted = true
				}
				break
			}
		}

		if selfCounted {
			// if self was counted in quorum, set its threshold and end the loop
			if firstCount {
				threshold = (NumQuorum * uint32(sortedWeights[lastCounted])) / uint32(selfStake)
			} else {
				threshold = NumQuorum
			}
			break
		} else {
			// self was not required for quorum, set the smallest validator in quorum to be offline
			offline[lastCounted] = true //make the smallest validator in current quorum offline; additional small validators required in the next count
		}
		firstCount = false
	}
	threshold = 4
	return threshold
}

func (h *QuorumIndexer) testingThreshold(online map[idx.ValidatorID]bool) float64 {
	//++TODO convert to int return value
	n := float64(h.validators.Len())

	// Cumulative count based threshold
	selfID := h.dagi.GetEvent(h.SelfParentEvent).Creator()
	selfStake := h.validators.GetWeightByIdx(h.validators.GetIdx(selfID))
	sortedWeights := h.validators.SortedWeights()
	sortedIDs := h.validators.SortedIDs()

	quorumCounter := h.validators.NewCounter()
	minNumQuorum := 0.0
	for _, ID := range sortedIDs {
		if online[ID] {
			quorumCounter.Count(ID)
			minNumQuorum++
		}
		if quorumCounter.HasQuorum() {
			break
		}
	}

	quorumCounter = h.validators.NewCounter()
	maxNumQuorum := 0.0
	for i := len(sortedIDs) - 1; i >= 0; i-- {
		ID := sortedIDs[i]
		if online[ID] {
			quorumCounter.Count(ID)
			maxNumQuorum++
		}
		if quorumCounter.HasQuorum() {
			break
		}
	}

	threshold := minNumQuorum / n * float64(sortedWeights[int(minNumQuorum-1)]) / float64(selfStake) // ensures largest validator in quourm have threhold <= quorum so that they produce events quickly so that frame rate is high
	// Tmin := 2 / n
	// c := Tmin + (numQuorum/n)/(float64(sortedWeights[0]-sortedWeights[int(numQuorum-1)]))*float64(sortedWeights[0])
	// threshold := -(numQuorum/n)/(float64(sortedWeights[0]-sortedWeights[int(numQuorum)-1]))*float64(selfStake) + c

	numLargerValidators := 0.0
	sumStake := h.validators.NewCounter()
	// InQuorum := false
	for i, stake := range sortedWeights {
		if sortedIDs[i] == selfID {
			if !sumStake.HasQuorum() {
				// InQuorum = true
			}
			sumStake.Count(sortedIDs[i])

		}
		if online[sortedIDs[i]] {
			if stake < selfStake {
				break
			}
			sumStake.Count(sortedIDs[i])

			numLargerValidators++
		}
		// +++TODO a check here to use self, even if self is somehow considered offline
	}

	// threshold := (numQuorum / n) * (n - numQuorum) / (n - numLargerValidators)

	if selfStake >= sortedWeights[int(minNumQuorum-1)] {
		threshold = minNumQuorum / n
		// threshold = (numQuorum - 1) / n
		// threshold = (numQuorum / n) * (n - numQuorum) / (n - numLargerValidators)
		// threshold = numQuorum / n * float64(sortedWeights[int(numQuorum-1)]) / float64(selfStake) // ensures largest validator in quourm have threhold <= quorum so that they produce events quickly so that frame rate is high
	} else {
		// threshold = numQuorum / n * float64(sortedWeights[int(numQuorum)]) / float64(selfStake)
		// threshold = (numQuorum / n) * (n - numQuorum) / (n - numLargerValidators)
		// threshold = 1
		threshold = maxNumQuorum / n
	}
	// SAFE THRESHOLD, using these thresholds the network will not stall provided quorum are progressing the DAG
	threshold = 0
	valThreshold := make([]float64, h.validators.Len())
	hasThreshold := make([]bool, h.validators.Len())
	offline := make([]bool, h.validators.Len()) //used to indicate scenario of some nodes offline
	numPasses := 0
	for {
		// +++TODO this doesn't account for multiple equal stakes
		quorumCounter = h.validators.NewCounter()
		NumQuorum := 0
		for i, ID := range sortedIDs {
			hasThreshold[i] = true
			if !offline[i] {
				quorumCounter.Count(ID)
				NumQuorum++
			}
			if quorumCounter.HasQuorum() {
				break
			}
		}
		idx := 0
		for i, _ := range sortedIDs {

			if !hasThreshold[i] {
				idx = i - 1
				break
			}
		}
		for i, ID := range sortedIDs {
			if !hasThreshold[i] {
				offline[i-1] = true
				break
			}
			if valThreshold[i] == 0 {
				if numPasses == 0 {
					valThreshold[i] = float64(NumQuorum) / float64(n) * float64(sortedWeights[idx]) / float64(sortedWeights[i])
				} else {
					valThreshold[i] = float64(NumQuorum) / float64(n)
				}
				if ID == selfID {
					if numPasses == 0 {
						threshold = float64(NumQuorum) / float64(n) * float64(sortedWeights[idx]) / float64(selfStake)
					} else {
						threshold = float64(NumQuorum) / float64(n)
					}
				}
			}

		}
		numPasses++
		// if float64(numPasses) > n/5 {
		// 	break
		// }
		if hasThreshold[len(hasThreshold)-1] {
			break
		}
	}
	if threshold == 0 {
		threshold = 2
	}
	// threshold = minNumQuorum / n
	// if threshold > numQuorum/n {
	// 	threshold = 2
	// }
	// if threshold > (n-1.0)/n {
	// 	threshold = (n - 1.0) / n
	// }
	// if threshold < 2/n {
	// 	threshold = 2 / n
	// }

	// if threshold < numQuorum/n {
	// 	threshold = numQuorum / n
	// }
	return threshold
}

func (h *QuorumIndexer) DAGProgressEventTimingCondition(chosenHeads hash.Events, online map[idx.ValidatorID]bool, time int) bool {
	// This function is used to determine if a new event should be created based upon DAG progress.
	// Primarily the function works by ordering the highest known event from each validator in the DAG according to the metric returned by RootKnowledgeByCount.
	// If the number of validators exceeding the metric of the most recent self event is greater than a threshold count, a new event should be created.
	// To prevent event creation from stalling, validators can also be counted if no event has been received since the creation of the previous self event,
	// (e.g. if the validator is offline), or if the validator's most recent event does not improve upon its previous event, indicating a potential attempt to
	// prevent other validators from meeting the above event creation criteria.
	timeThreshParam := 999999999 // +++TODO
	ePrev := h.dagi.GetEvent(h.SelfParentEvent)
	selfFrame := ePrev.Frame()
	selfIdx := h.validators.GetIdx(ePrev.Creator())

	var kGreaterCount uint32 = 0
	kGreaterStake := h.validators.NewCounter()
	kPrev := h.RootKnowledgeByCount(selfFrame, h.SelfParentEvent, nil)        // calculate metric of root knowledge for previous self event
	kNew := h.RootKnowledgeByCount(selfFrame, h.SelfParentEvent, chosenHeads) // calculate metric of root knowledge for new event under consideration

	if kNew > kPrev { // this condition prevents the function always returning true when less than quorum nodes are online, and no DAG progress can be made
		for i, e := range h.validatorHighestEvents {
			if e.event != nil {
				eFrame := h.dagi.GetEvent(e.event.ID()).Frame()
				switch {
				case eFrame > selfFrame: // validator's frame is ahead of self
					kGreaterCount++
					kGreaterStake.Count(e.event.Creator())
					break
				case time-h.validatorHighestEvents[selfIdx].time > timeThreshParam && time-h.validatorHighestEvents[i].time > timeThreshParam: // max time interval between receiveing events from this validator exceeded
					kGreaterCount++
					kGreaterStake.Count(e.event.Creator())
					break
				case h.validatorHighestEvents[i].kChange == false: // most recent event received from i did not increase k DAG progress metric
					kGreaterCount++
					kGreaterStake.Count(e.event.Creator())
					break
				case eFrame == selfFrame:
					k := h.RootKnowledgeByCount(selfFrame, e.event.ID(), nil)
					if k >= kPrev {
						kGreaterCount++
						kGreaterStake.Count(e.event.Creator())
					}
				}
			}
		}

		if kGreaterCount >= h.Threshold() {
			return true // self should create a new event
		}
	}
	return false // self should not create a new event
	return true
}
