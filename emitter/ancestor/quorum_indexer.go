package ancestor

import (
	"fmt"
	"math"
	"math/rand"
	"sort"

	"github.com/Fantom-foundation/lachesis-base/abft"
	"github.com/Fantom-foundation/lachesis-base/abft/dagidx"
	"github.com/Fantom-foundation/lachesis-base/abft/election"
	"github.com/Fantom-foundation/lachesis-base/hash"
	"github.com/Fantom-foundation/lachesis-base/inter/dag"
	"github.com/Fantom-foundation/lachesis-base/inter/idx"
	"github.com/Fantom-foundation/lachesis-base/inter/pos"
	"github.com/Fantom-foundation/lachesis-base/vecfc"
)

type perfMeasurePhase struct {
}

type globalPhase struct {
	inOperation        bool
	start              idx.Frame
	end                idx.Frame
	allPhaseDuration   idx.Frame
	measurementStart   idx.Frame
	measurementEnd     idx.Frame
	measurementPending bool
}

func (g *globalPhase) Active(maxKnownFrame idx.Frame) bool {

	if maxKnownFrame >= g.start && maxKnownFrame <= g.end {
		return true
	}
	return false
}

func (g *globalPhase) newPhase() {
	//update start and end
	g.start += g.allPhaseDuration
	g.end += g.allPhaseDuration
}

func (r *globalPhase) startPhase(maxKnownFrame idx.Frame) bool {
	return true
}

type randomPhase struct {
	start               idx.Frame         // frame to start random phase
	end                 idx.Frame         // frame to end random phase
	allPhaseDuration    idx.Frame         // duration of random phase in frames
	fastStart           idx.Frame         // frame to start fast sub-phase
	fastEnd             idx.Frame         // frame to end fast sub-phase
	fastDuration        idx.Frame         // duration of fast sub-phase in frames
	measurementStart    idx.Frame         // frame to start measurement
	measurementEnd      idx.Frame         // frame to end measurement
	measurementComplete bool              // flag to indicate measurement has been completed
	fast                bool              // flag to indicate if validator should be emitting fast
	higherPerfValIDs    []idx.ValidatorID // list of validators that were higher performing than self in fast sub-phase
}

func (r *randomPhase) newPhase() {
	//update start and end
	r.start += r.allPhaseDuration
	r.end += r.allPhaseDuration
	duration := r.end - r.start
	r.fastStart = r.start + idx.Frame(rand.Intn(int(duration-r.fastDuration)))
	r.fastEnd = r.fastStart + r.fastDuration
}

func (r *randomPhase) emitFast(maxKnownFrame idx.Frame) bool {
	if maxKnownFrame >= r.fastStart && maxKnownFrame <= r.fastEnd {
		r.fast = true
		return true
	}
	r.fast = false
	return false
}

type ChangeFastToSlow struct {
	pending            bool
	superiorValidators []idx.ValidatorID
}

type FastOrSlow struct {
	randomPhase randomPhase
	globalPhase globalPhase

	ChangeFastToSlow bool
	Fast             bool
	tempFast         bool
	NumHigherPerf    uint32
	Update           bool

	minFastStake          pos.Weight
	minNumExceedingQuorum int
}

func (f *FastOrSlow) newFrame(maxKnownFrame idx.Frame) {

	//check if random measurement sub-phase has ended
	if f.randomPhase.fast { //previously fast
		if !f.randomPhase.emitFast(maxKnownFrame) { //no longer fast
			// random measurement sub-phase has ended,
			// prepare for performance measurement calculation
			f.randomPhase.measurementComplete = false
			f.randomPhase.measurementStart = f.randomPhase.fastStart
			f.randomPhase.measurementEnd = f.randomPhase.fastEnd

			f.randomPhase.fast = false
			f.tempFast = false
		}
	}

	// check if global performance measurement phase has ended
	prevActive := f.globalPhase.inOperation
	if prevActive && !f.globalPhase.Active(maxKnownFrame) {

		// prepare for performance measurement calculation
		// f.globalPhase.measurementPending = false
		f.globalPhase.measurementStart = f.globalPhase.start
		f.globalPhase.measurementEnd = f.globalPhase.end

		// update start and end of phases to start over
		// f.randomPhase.newPhase()
	}

	if maxKnownFrame == f.globalPhase.end+1 {
		f.globalPhase.newPhase()
		f.randomPhase.newPhase()
	}

	// update random, secret performance measurement phase using maxFrame
	f.tempFast = f.randomPhase.emitFast(maxKnownFrame)
}

type rootLowestAfter struct {
	event idx.Event
	root  dag.Event
}

type sortedKIdx []KIdx
type KIdx struct {
	K    float64
	Root election.RootAndSlot
}

type Sigmoid struct {
	Centre float64
	Slope  float64
}

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
	k     int
	khat  float64
}

type validatorHighestEvent struct {
	event   dag.Event
	time    uint32 //+++TODO CHANGE TO APPROPRIATE TIME DATATYPE
	kChange bool
	k       int
}

type EventTiming struct {
	validatorHighestEvents []validatorHighestEvent
	hasRoot                [][]bool
	numFramesToCheck       int
	newestFrameChecked     idx.Frame
	newestFrameIndex       int
	maxKnownFrame          idx.Frame
	fast                   []bool
	minFastStake           pos.Weight
	threshold              float64
}

type Store interface {
	GetFrameRoots(f idx.Frame) []election.RootAndSlot
	GetLastDecidedFrame() idx.Frame
}

type DagIndex interface {
	dagidx.VectorClock
	dagidx.ForklessCause
	GetEvent(hash.Event) dag.Event
	GetLowestAfter(hash.Event) *vecfc.LowestAfterSeq
}
type DiffMetricFn func(median, current, update idx.Event, validatorIdx idx.Validator) Metric

type QuorumIndexer struct {
	dagi       DagIndex
	store      Store
	validators *pos.Validators

	randParent      *rand.Rand
	SelfParentEvent hash.Event

	FastOrSlow FastOrSlow

	eventTiming EventTiming
	sigmoid     Sigmoid
}

func NewQuorumIndexer(validators *pos.Validators, lchs *abft.TestLachesis) *QuorumIndexer {
	var eventTiming EventTiming
	eventTiming.validatorHighestEvents = make([]validatorHighestEvent, validators.Len())
	eventTiming.maxKnownFrame = 0
	eventTiming.fast = make([]bool, validators.Len())
	eventTiming.numFramesToCheck = 5
	eventTiming.hasRoot = make([][]bool, validators.Len())
	for i := 0; i < int(validators.Len()); i++ {
		eventTiming.hasRoot[i] = make([]bool, eventTiming.numFramesToCheck)
	}
	eventTiming.newestFrameChecked = 0
	eventTiming.newestFrameIndex = 0
	eventTiming.minFastStake = (validators.TotalWeight() * 9) / 10 //% of total stake
	// eventTiming.threshold = threshold

	var FastOrSlow FastOrSlow
	FastOrSlow.Fast = false // +++TODO CHANGE INITIALISATION
	FastOrSlow.tempFast = false
	// FastOrSlow.frameStartTest = 10 + idx.Frame(rand.Intn(200))
	// FastOrSlow.testDuration = 25
	FastOrSlow.Update = false

	FastOrSlow.minFastStake = pos.Weight(0.7 * float64(validators.TotalWeight()))
	FastOrSlow.minNumExceedingQuorum = 0 //+++TODO decide what this should be! Change so that if the largest x validtors go offline, there are sufficient smaller validators to prevent stalling?

	var allPhaseDuration idx.Frame = 100
	var randomPhase randomPhase
	randomPhase.allPhaseDuration = allPhaseDuration
	randomPhase.start = 0
	randomPhase.end = allPhaseDuration - 21
	randomPhase.fastDuration = 20
	duration := randomPhase.end - randomPhase.start
	randomPhase.fastStart = randomPhase.start + idx.Frame(rand.Intn(int(duration-randomPhase.fastDuration))) // initialise random fast sub-phase
	randomPhase.fastEnd = randomPhase.fastStart + randomPhase.fastDuration
	randomPhase.measurementComplete = false
	randomPhase.measurementStart = randomPhase.fastStart
	randomPhase.measurementEnd = randomPhase.fastEnd
	randomPhase.fast = false

	var globalPhase globalPhase
	globalPhase.inOperation = false
	globalPhase.allPhaseDuration = allPhaseDuration
	globalPhase.start = randomPhase.end + 1
	globalPhase.end = globalPhase.allPhaseDuration - 1
	globalPhase.measurementStart = globalPhase.start
	globalPhase.measurementEnd = globalPhase.end
	globalPhase.measurementPending = false

	FastOrSlow.globalPhase = globalPhase
	FastOrSlow.randomPhase = randomPhase

	return &QuorumIndexer{
		dagi:       lchs.DagIndex,
		store:      lchs.Store,
		validators: validators,

		randParent:  rand.New(rand.NewSource(0)), // +++TODO rand.New(rand.NewSource(time.Now().UnixNano())),
		eventTiming: eventTiming,
		FastOrSlow:  FastOrSlow,
		// sigmoid:     sigmoid,
	}
}

func (h *QuorumIndexer) Performance() {

	var frameRange idx.Frame = 5
	if !h.FastOrSlow.randomPhase.measurementComplete { // check if a random phase measurement calculation is pending
		if h.store.GetLastDecidedFrame() >= h.FastOrSlow.randomPhase.measurementEnd { // check if DAG is ready for performance measurement calculation
			selfID := h.dagi.GetEvent(h.SelfParentEvent).Creator()
			selfStake := h.validators.Get(selfID)
			prevFast := h.FastOrSlow.Fast //record previous state
			h.FastOrSlow.Fast = false     // default to slow

			higherPerfStakeFrac, _, numExcess, higherPerfValIDs := h.calculateSelfPerformance(h.FastOrSlow.randomPhase.measurementStart, h.FastOrSlow.randomPhase.measurementEnd-frameRange-1, frameRange)
			h.FastOrSlow.randomPhase.higherPerfValIDs = higherPerfValIDs
			if higherPerfStakeFrac < float64(h.FastOrSlow.minFastStake) { // +++TODO choose appropriate value here
				// insufficient stake of higher performing validators
				h.FastOrSlow.Fast = true // set self as fast
				if !prevFast {
					fmt.Println("SLOW to FAST: ", selfStake, " ", float64(selfStake)/float64(h.validators.TotalWeight()))
				}
			}
			if numExcess < h.FastOrSlow.minNumExceedingQuorum { // +++TODO choose appropriate value here. % of total number of validators?
				// insufficient number of higher performing validators in excess of quorum
				h.FastOrSlow.Fast = true // set self as fast
				if !prevFast {
					fmt.Println("SLOW to FAST: ", selfStake, " ", float64(selfStake)/float64(h.validators.TotalWeight()))
				}
			}
			if prevFast && !h.FastOrSlow.Fast {
				// self was previously fast, but this performance measurement indicates self should be slow
				h.FastOrSlow.globalPhase.measurementPending = true // flag to indicate a measurement is required
			}
			h.FastOrSlow.randomPhase.measurementComplete = true
		}
	}

	if h.FastOrSlow.globalPhase.measurementPending { // check if a global phase measurement calculation is pending to confirm change from fast to slow
		if h.store.GetLastDecidedFrame() >= h.FastOrSlow.globalPhase.measurementEnd { // check if DAG is ready for performance measurement calculation

			_, _, _, higherPerfValIDs := h.calculateSelfPerformance(h.FastOrSlow.globalPhase.measurementStart, h.FastOrSlow.globalPhase.measurementEnd-frameRange-1, frameRange)
			if h.compareValidatorLists(h.FastOrSlow.randomPhase.higherPerfValIDs, higherPerfValIDs) {
				// the set of higher performing validators in the secret random phase were also higher performing in the global phase
				selfID := h.dagi.GetEvent(h.SelfParentEvent).Creator()
				selfStake := h.validators.Get(selfID)
				fmt.Println("FAST to SLOW: ", selfStake, " ", float64(selfStake)/float64(h.validators.TotalWeight()))
				h.FastOrSlow.Fast = false // change to slow
			}
			h.FastOrSlow.globalPhase.measurementPending = false // performance measurement has been completed
		}
	}
}

func (h *QuorumIndexer) calculateSelfPerformance(frameStart, frameEnd, frameRange idx.Frame) (float64, uint32, int, []idx.ValidatorID) {
	// This function ranks the performance of self validator in comparison to all other validators.
	// It outputs the fraction of stake that performs better that self, the stake that performs better than self, and
	// the number of validators that perform better than self, and are in excess to quorum stake, i.e. dont count first quorum validators
	// we use this to ensure a minimum number of validators in excess of quorum emit fast, to ensure that at least that many must be disrupted to significantly impact consensus

	if !h.SelfParentEvent.IsZero() { // check that the most recent self event is not a leaf event
		selfID := h.dagi.GetEvent(h.SelfParentEvent).Creator()
		selfStake := h.validators.Get(selfID)
		higherPerfStake := h.validators.NewCounter()
		higherPerfValIDs := make([]idx.ValidatorID, 0)
		numExcess := 0 // the number of validators that are higher performance than self, but in excess of the number required for quorum stake

		selfPerf := h.ValidatorPerformance(selfID, frameStart, frameEnd, frameRange)
		for _, valID := range h.validators.IDs() {
			// fmt.Println("Val: ", i)
			valPerf := h.ValidatorPerformance(valID, frameStart, frameEnd, frameRange)
			// fmt.Println(h.validators.GetIdx(valID), " Perf: ", valPerf)
			if valID != selfID {

				if valPerf < selfPerf { // validator performs better than self, smaller metric means higher performance
					if higherPerfStake.HasQuorum() { // if there are already quorum validators, then count as excess
						numExcess++
					}
					higherPerfValIDs = append(higherPerfValIDs, valID)
					higherPerfStake.Count(valID)

				} else if valPerf == selfPerf { // if validator has equal performance to self
					valStake := h.validators.Get(valID)
					if valStake > selfStake { // prefer the larger staked validator
						if higherPerfStake.HasQuorum() { // if there are already quorum validators, then count as excess
							numExcess++
						}
						higherPerfValIDs = append(higherPerfValIDs, valID)
						higherPerfStake.Count(valID)
					}
				}
			}
		}
		return float64(higherPerfStake.Sum()) / float64(h.validators.TotalWeight()), uint32(higherPerfStake.Sum()), numExcess, higherPerfValIDs
	}
	return 1.0, uint32(h.validators.TotalWeight()), 0, h.validators.IDs()
}

func (h *QuorumIndexer) EventMedianTime(event dag.Event) uint32 {
	// This function calculates the stake weighted median creation time of event.
	// This corresponds to the stake weighted median creation time of the set of highest before events from all validator
	// +++TODO/Note, for simulation purposes we ignore cheaters etc unlike in go-opera!!!

	var median uint32 = 0
	if event.Seq() > 0 { // not a leaf event
		// event may not be in the DAG, use its parents to find highestBefore
		highestBefore := make(map[idx.Validator]idx.Event)
		for _, parent := range event.Parents() {
			highestBeforeOfParent := h.dagi.GetMergedHighestBefore(parent) // highestBefore sequence numbers of event
			for _, valIdx := range h.validators.Idxs() {
				if highestBeforeOfParent.Get(valIdx).Seq() > highestBefore[valIdx] {
					highestBefore[valIdx] = highestBeforeOfParent.Get(valIdx).Seq()
				}
			}

		}

		highestEvents := make([]dag.Event, 0, h.validators.Len()) // create a slice of events (to be found)

		// now find the dag.Event object for each highest before event
		for _, valIdx := range h.validators.Idxs() {
			seq := highestBefore[valIdx]
			if seq > 0 {
				hbEvent := h.eventTiming.validatorHighestEvents[valIdx].event
				for ; hbEvent.Seq() > seq; hbEvent = h.dagi.GetEvent(*hbEvent.SelfParent()) {
				}
				highestEvents = append(highestEvents, hbEvent)
			}
		}

		// at this point we now have the highest before event for each validator
		// sort highest before events according to creation time
		sort.Slice(highestEvents, func(i, j int) bool {
			a, b := highestEvents[i], highestEvents[j]
			return a.CreationTime() < b.CreationTime()
		})

		// Calculate weighted median from the sorted events
		medianWeight := h.validators.TotalWeight() / 2
		var currWeight pos.Weight

		for _, highest := range highestEvents {
			currWeight += h.validators.Get(highest.Creator())
			if currWeight >= medianWeight {
				median = highest.CreationTime()
				break
			}
		}
	}

	return median
}

func (h *QuorumIndexer) FastOrSlowThreshold() uint32 {
	// Sets the event timing ordering threshold
	var threshold uint32
	if h.FastOrSlow.Fast || h.FastOrSlow.tempFast {
		//the validator is part of the set of high performance, fast emitting validators
		selfID := h.dagi.GetEvent(h.SelfParentEvent).Creator()
		selfStake := h.validators.GetWeightByIdx(h.validators.GetIdx(selfID))
		sortedWeights := h.validators.SortedWeights()
		sortedIDs := h.validators.SortedIDs()

		//find the quorumth validator, starting with largest validators
		weightCounter := h.validators.NewCounter()
		quorumValidator := 0
		for _, ID := range sortedIDs {
			weightCounter.Count(ID)
			if weightCounter.HasQuorum() {
				break
			}
			quorumValidator++
		}
		// set threshold based on stake, so that large validators have a lower threshold and emit events more frequently
		threshold = uint32(float64(h.validators.Quorum()) * float64(sortedWeights[quorumValidator]) / float64(selfStake)) //give larger stake validators a smaller threshold so they produce events faster than smaller stake validators
		if threshold > uint32(h.validators.Quorum()) {
			// maximum threshold is quorum (to prevent stalling)
			threshold = uint32(h.validators.Quorum())
		}
	} else {
		// the validator is not part of the set of high performance, fast emitting validators
		threshold = uint32(h.validators.TotalWeight() + 1)
	}
	return threshold

}

func (h *QuorumIndexer) thresholdFrame(stakeThreshold float64) idx.Frame {
	// this function is used to find the highest frame that has been reached by a stakeThreshold fraction of validators
	type frameStake struct { //used to record the highest frame reached by a validator
		frame idx.Frame
		valID idx.ValidatorID
	}
	highestFramesStakes := make([]frameStake, h.validators.Len())
	for i, event := range h.eventTiming.validatorHighestEvents {
		if event.event != nil {
			highestFramesStakes[i].frame = event.event.Frame()
			highestFramesStakes[i].valID = event.event.Creator()
		}
	}
	//sort each validator by its highest frame
	sort.Slice(highestFramesStakes, func(i, j int) bool { return highestFramesStakes[i].frame > highestFramesStakes[j].frame }) // sort weights in order

	// starting from the validator with the highest frame, count stakeThreshold validators
	// the threshold frame is determined by the last validator counted
	thresholdFrameStake := h.validators.NewCounter()
	var thresholdFrame idx.Frame
	for _, frameStake := range highestFramesStakes {
		thresholdFrameStake.Count(frameStake.valID)
		if float64(thresholdFrameStake.Sum())/float64(h.validators.TotalWeight()) > stakeThreshold {
			thresholdFrame = frameStake.frame
			break
		}
	}
	thresholdFrame -= 1 // we want completed, not in progress frames, so reduce by one frame

	// ensure self is at least up to thresholdFrame
	// if !h.SelfParentEvent.IsZero() {
	// 	selfID := h.dagi.GetEvent(h.SelfParentEvent).Creator()
	// 	selfIdx := h.validators.GetIdx(selfID)
	// 	selfFrame := h.eventTiming.validatorHighestEvents[selfIdx].event.Frame() - 1
	// 	if thresholdFrame > selfFrame {
	// 		thresholdFrame = selfFrame
	// 	}
	// }
	return thresholdFrame
}

func (h *QuorumIndexer) numForQuorum(valList []idx.ValidatorID) (int, int) {
	// return the minimum number of validators in valList required for quorum and
	// the remaining number of validators in excess of quorum
	// If the total stake of validators in valList is less than quorum, return zero

	// ensure there are no duplicates in valList
	for i, valA := range valList {
		for j, valB := range valList[i+1:] {
			if valA == valB {
				// valB is a duplicate, remove it by replacing with the last element and shortening the slice
				valList[j] = valList[len(valList)-1]
				valList = valList[:len(valList)-1]
			}
		}
	}

	// sort valList according to stake from largest to smallest
	sort.Slice(valList, func(i, j int) bool {
		a, b := valList[i], valList[j]
		return h.validators.Get(a) < h.validators.Get(b)
	})

	// count minimum number required for quourm
	quorumCounter := h.validators.NewCounter()
	for _, valID := range valList {
		quorumCounter.Count(valID)
		if quorumCounter.HasQuorum() {
			return quorumCounter.NumCounted(), len(valList) - quorumCounter.NumCounted()
		}
	}

	return 0, 0
}

func (h *QuorumIndexer) compareValidatorLists(valListA, valListB []idx.ValidatorID) bool {
	// return true if the validators in list A are all contained in list B and
	// there are sufficient validators in list A to meet the minimum Fast criteria

	// ensure there are no duplicates in valListA
	for i, valA := range valListA {
		for j, valB := range valListA[i+1:] {
			if valA == valB {
				// valB is a duplicate, remove it by replacing with the last element and shortening the slice
				valListA[j] = valListA[len(valListA)-1]
				valListA = valListA[:len(valListA)-1]
			}
		}
	}

	// find the validators in valListA that are also in valListB
	var foundList []idx.ValidatorID
	foundCtr := h.validators.NewCounter()
	for _, valA := range valListA {
		for _, valB := range valListB {
			if valA == valB {
				foundList = append(foundList, valA)
				foundCtr.Count(valA)
				break
			}
		}
	}

	// check minimum Fast criteria
	if foundCtr.Sum() >= h.FastOrSlow.minFastStake {
		_, numExceedingQuorum := h.numForQuorum(foundList)
		if numExceedingQuorum >= h.FastOrSlow.minNumExceedingQuorum {
			return true
		}
	}

	return false
}

// func (h *QuorumIndexer) CheckFastOrSlowOLD() {
// 	// This function is used to determine if self validator is sufficiently high performance (measure by a DAG progress metric)
// 	// to be included in the set of validator that use the performance event timing conditions to emit fast and maintain high frame rate/low TTF
// 	// Slow validators should use a separate event timing condition based on transaction demand and gas availablity
// 	minNumExcess := 3     // the number of validators in excess of quorum in the set of validators that emit fast.
// 	stakeThreshold := 0.7 // the minimum stake fraction of validators that should be emit fast. This should be above quorum (2/3+1)
// 	// +++TODO, it may be preferable to count the stake of the largest minNumExcess validators s_excess, and require that stakeThreshold be quorum + s_excess
// 	// that way if a few large validators happen to be disrupted, then there are sufficient smaller ones to maintain a fast quorum. However, since the stake distribution
// 	// is heavily concentrated in large validators, this may not be possible.

// 	thresholdFrame := h.thresholdFrame(stakeThreshold)
// 	if h.eventTiming.maxKnownFrame >= h.FastOrSlow.frameStartTest && thresholdFrame < h.FastOrSlow.frameStartTest+h.FastOrSlow.testDuration && !h.FastOrSlow.Update {
// 		// it is time to start emitting fast to check self validator performance, and if self validator should emit fast, or not
// 		self := h.dagi.GetEvent(h.SelfParentEvent).Creator()

// 		if !h.FastOrSlow.Update { // some testing /debugging output
// 			fmt.Println("Frame: ", h.eventTiming.maxKnownFrame, " Fast: ", h.validators.Get(self), " ", float64(h.validators.Get(self))/float64(h.validators.TotalWeight()))
// 		}

// 		h.FastOrSlow.Update = true // flag to indicate the validator has switched to emit fast temporarily to test its fast performance
// 		h.FastOrSlow.Fast = true   // flag to indicate the validator should emit fast

// 	}
// 	if thresholdFrame > h.FastOrSlow.frameStartTest+h.FastOrSlow.testDuration && h.FastOrSlow.Update {
// 		// testing frames are complete, now compare self performance to other validators durign the testing period
// 		stakeBetterThanSelf, numHigherPerf, numExcess := h.RankSelfPerformance()
// 		h.FastOrSlow.NumHigherPerf = numHigherPerf
// 		self := h.dagi.GetEvent(h.SelfParentEvent).Creator()
// 		if stakeBetterThanSelf > stakeThreshold && numExcess >= minNumExcess {
// 			// self performance was not sufficient to be included in the set of fast validators
// 			fmt.Println("Slowed: ", h.validators.Get(self), " ", float64(h.validators.Get(self))/float64(h.validators.TotalWeight()), "Stake Above: ", stakeBetterThanSelf)
// 			h.FastOrSlow.Fast = false

// 		} else {
// 			// self performance was sufficient to be included in the set of fast validators
// 			h.FastOrSlow.Fast = true
// 			fmt.Println("Didn't slow: ", h.validators.Get(self), " ", float64(h.validators.Get(self))/float64(h.validators.TotalWeight()), "Stake Above: ", stakeBetterThanSelf)
// 		}
// 		h.FastOrSlow.Update = false // turn off the test flag

// 		h.FastOrSlow.frameStartTest += h.FastOrSlow.testDuration + idx.Frame(rand.Intn(int(h.validators.Len())*int(h.FastOrSlow.testDuration))) // select a frame to start the next test period

// 		if h.FastOrSlow.frameStartTest > 2700 { // this is for testing purposes only, to ensure validator stop testing before the end of the simulation
// 			h.FastOrSlow.frameStartTest = 99999999
// 		}
// 	}
// }

// func (h *QuorumIndexer) RankSelfPerformance() (float64, uint32, int) {
// 	// This function ranks the performance of self validator in comparison to all other validators.
// 	// It outputs the fraction of stake that performs better that self, the stake that performs better than self, and
// 	// the number of validators that perform better than self, and are in excess to quorum stake, i.e. dont count first quorum validators
// 	// we use this to ensure a minimum number of validators in excess of quorum emit fast, to ensure that at least that many must be disrupted to significantly impact consensus

// 	if !h.SelfParentEvent.IsZero() {
// 		selfID := h.dagi.GetEvent(h.SelfParentEvent).Creator()
// 		selfStake := h.validators.Get(selfID)
// 		higherPerfStake := h.validators.NewCounter()
// 		numExcess := 0 // the number of validators that are higher performance than self, but in excess of the number required for quorum stake

// 		selfPerf := h.ValidatorPerformance(selfID)
// 		for _, valID := range h.validators.IDs() {
// 			// fmt.Println("Val: ", i)
// 			valPerf := h.ValidatorPerformance(valID)
// 			// fmt.Println(h.validators.GetIdx(valID), " Perf: ", valPerf)
// 			if valID != selfID {

// 				if valPerf < selfPerf { // validator performs better than self, smaller metric means higher performance
// 					if higherPerfStake.HasQuorum() { // if there are already quorum validators, then count as excess
// 						numExcess++
// 					}
// 					higherPerfStake.Count(valID)

// 				} else if valPerf == selfPerf { // if validators have equal performance to self
// 					valStake := h.validators.Get(valID)
// 					if valStake > selfStake { // prefer the larger staked validator
// 						higherPerfStake.Count(valID)

// 						if higherPerfStake.HasQuorum() { // if there are already quorum validators, then count as excess
// 							numExcess++
// 						}
// 					}
// 				}
// 			}
// 		}
// 		return float64(higherPerfStake.Sum()) / float64(h.validators.TotalWeight()), uint32(higherPerfStake.Sum()), numExcess
// 	}
// 	return 1.0, uint32(h.validators.TotalWeight()), 0
// }

func (h *QuorumIndexer) ValidatorPerformance(valID idx.ValidatorID, minFrame, maxFrame, frameRange idx.Frame) float64 {
	// the function calculates a metric for the performance of valID
	// The metric is based on the root knowledge metric
	// For a frame f, find the event e_q at which valID knows at least quorum roots of frame f.
	// Sum the k metrics at which each validator has e_q in its subgraph
	// Calculate this across a set of recent frames
	// A lower metric means higher performance
	// It is important for consensus for validators to know roots from other validators, hence
	// the use of e_q.
	// frameRange is used to ensure that for each frame f, the calculation is conducted using the same number of frames
	// thus the DAG must will be searched up to maxframe+frameRange

	TotalStake := float64(h.validators.TotalWeight())
	metric := 0.0 // initialise the metric

	// loop over range of frames
	for frame := maxFrame; frame >= minFrame && frame > 0; frame-- {

		frameMetric := 0.0 // the metric for the current frame

		eKnowsQRoots, found := h.FindEventKnowsQRoots(frame, valID) // find self's event that known quorum roots in the current frame
		if found {
			// found == true indicates that e_q exists for the current frame
			lowestAfter := h.dagi.GetLowestAfter(eKnowsQRoots.ID()) // use LowestAfter of e_q to find events from each validator that have e_q in their subgraph

			// loop over all validators
			for _, valIdxLA := range h.validators.Idxs() {
				eventLASeq := lowestAfter.Get(valIdxLA) // sequence number of lowest after event

				// use sequence number to find the LowestAfter event hash by starting at the highest known event
				eventLA := h.eventTiming.validatorHighestEvents[valIdxLA].event
				stakeLA := h.validators.GetWeightByIdx(valIdxLA)
				if eventLASeq > 0 { // there is an event the has e_q in its subgraph
					// we have the LA event sequence number, but need the event ID
					// starting from the highest known event, work backward until the LA event sequence number is found
					for ; eventLA.Seq() > eventLASeq; eventLA = h.dagi.GetEvent(*eventLA.SelfParent()) {
					}

					// the lowest after event may be in a later frame, so add k metric contributions from all frames
					kLA := 0.0
					for f := frame; f <= eventLA.Frame() && f < frame+frameRange; f++ {
						kLA += h.progressTowardNewRoot(f, eventLA.ID(), nil)
					}
					// frameMetric += kLA / (float64(maxFrame-frame) + 1.0) * float64(stakeLA) / TotalStake
					// frameMetric += (kLA - (kVal - kRoot)) / (float64(maxFrame-frame) + 1.0) * float64(stakeLA) / TotalStake
					frameMetric += kLA * float64(stakeLA) / (float64(frameRange) + 1.0) / TotalStake // weight the metric by the validator stake, and normalise by number of frames (max k metric)
				} else { // there is not an event that has e_q in its subgraph
					// frameMetric += (float64(maxFrame-frame) + 1.0 - (kVal - kRoot)) / (float64(maxFrame-frame) + 1.0) * float64(stakeLA) / TotalStake
					frameMetric += float64(stakeLA) / TotalStake // add the maximum metric
				}
			}
		} else {
			// found == false indicates that e_q does not exist for the current frame.
			frameMetric += 1.0 // Set the maximum metric for this frame
		}
		metric += frameMetric // add each frame's contribution to the metric
	}
	return metric / float64(maxFrame-minFrame+1) // normalise the metric by the number of frames used in the calculation

}
func (h *QuorumIndexer) ValidatorPerformanceOLD(valID idx.ValidatorID, minFrame, maxFrame idx.Frame) float64 {
	// the function calculates a metric for the performance of valID
	// The metric is based on the root knowledge metric
	// For a frame f, find the event e_q at which valID knows at least quorum roots of frame f.
	// Sum the k metrics at which each validator has e_q in its subgraph
	// Calculate this across a set of recent frames
	// A lower metric means higher performance
	// It is important for consensus for validators to know roots from other validators, hence
	// the use of e_q.

	TotalStake := float64(h.validators.TotalWeight())
	metric := 0.0 // initialise the metric

	// loop over range of frames
	for frame := maxFrame; frame >= minFrame && frame > 0; frame-- {

		frameMetric := 0.0 // the metric for the current frame

		eKnowsQRoots, found := h.FindEventKnowsQRoots(frame, valID) // find self's event that known quorum roots in the current frame
		if found {
			// found == true indicates that e_q exists for the current frame
			lowestAfter := h.dagi.GetLowestAfter(eKnowsQRoots.ID()) // use LowestAfter of e_q to find events from each validator that have e_q in their subgraph

			// loop over all validators
			for _, valIdxLA := range h.validators.Idxs() {
				eventLASeq := lowestAfter.Get(valIdxLA) // sequence number of lowest after event

				// use sequence number to find the LowestAfter event hash by starting at the highest known event
				eventLA := h.eventTiming.validatorHighestEvents[valIdxLA].event
				stakeLA := h.validators.GetWeightByIdx(valIdxLA)
				if eventLASeq > 0 { // there is an event the has e_q in its subgraph
					// we have the LA event sequence number, but need the event ID
					// starting from the highest known event, work backward until the LA event sequence number is found
					for ; eventLA.Seq() > eventLASeq; eventLA = h.dagi.GetEvent(*eventLA.SelfParent()) {
					}

					// the lowest after event may be in a later frame, so add k metric contributions from all frames
					kLA := 0.0
					for f := frame; f <= eventLA.Frame() && f <= maxFrame; f++ {
						kLA += h.progressTowardNewRoot(f, eventLA.ID(), nil)
					}
					// frameMetric += kLA / (float64(maxFrame-frame) + 1.0) * float64(stakeLA) / TotalStake
					// frameMetric += (kLA - (kVal - kRoot)) / (float64(maxFrame-frame) + 1.0) * float64(stakeLA) / TotalStake
					frameMetric += kLA * float64(stakeLA) / (float64(maxFrame-frame) + 1.0) / TotalStake // weight the metric by the validator stake, and normalise by number of frames (max k metric)
				} else { // there is not an event that has e_q in its subgraph
					// frameMetric += (float64(maxFrame-frame) + 1.0 - (kVal - kRoot)) / (float64(maxFrame-frame) + 1.0) * float64(stakeLA) / TotalStake
					frameMetric += float64(stakeLA) / TotalStake // add the maximum metric
				}
			}
		} else {
			// found == false indicates that e_q does not exist for the current frame.
			frameMetric += 1.0 // Set the maximum metric for this frame
		}
		metric += frameMetric // add each frame's contribution to the metric
	}
	return metric / float64(maxFrame-minFrame+1) // normalise the metric by the number of frames used in the calculation

}
func (h *QuorumIndexer) FindEventKnowsQRoots(frame idx.Frame, valID idx.ValidatorID) (dag.Event, bool) {
	// find lowest event from validator that has quorum roots from frame in its subgraph
	// +++TODO incorporate caching to improve performance if this function will be called frequently
	// +++TODO problems with forks?
	valIdx := h.validators.GetIdx(valID)
	roots := h.store.GetFrameRoots(frame)
	var valLowestAfter []rootLowestAfter
	// for each root in the frame, find the lowest event (sequence number) of validator that has the root in its subgraph
	for _, root := range roots {
		lowestAfterRoot := h.dagi.GetLowestAfter(root.ID)
		if lowestAfterRoot.Get(valIdx) > 0 { // ensure that there is a lowest after event
			var temp rootLowestAfter
			temp.root = h.dagi.GetEvent(root.ID)
			temp.event = lowestAfterRoot.Get(valIdx)
			valLowestAfter = append(valLowestAfter, temp)
		}
	}

	// sort so that the lowest events are first
	sort.SliceStable(valLowestAfter, func(i, j int) bool {
		return valLowestAfter[i].event < valLowestAfter[j].event
	})

	// find the lowest event that knows at least quorum roots
	stakeCtr := h.validators.NewCounter()
	var knowsQuorum idx.Event
	for _, event := range valLowestAfter {
		stakeCtr.Count(event.root.Creator())
		knowsQuorum = event.event
		if stakeCtr.HasQuorum() {
			break
		}
	}
	hEvent := h.eventTiming.validatorHighestEvents[valIdx]
	knowsQuorumDAGEvent := hEvent.event
	if knowsQuorum > 0 {
		for ; knowsQuorumDAGEvent.Seq() > knowsQuorum; knowsQuorumDAGEvent = h.dagi.GetEvent(*knowsQuorumDAGEvent.SelfParent()) {
		}
	}
	if stakeCtr.HasQuorum() {
		return knowsQuorumDAGEvent, true
	} else {
		return knowsQuorumDAGEvent, false // return false if no event knows quroum roots
	}
}

func (h *QuorumIndexer) ProcessEvent(event dag.Event, selfEvent bool, time uint32) {
	// This function should be called each time a new event is added to the DAG.
	// This function records quantities that are needed for event timing
	print := false
	if !h.SelfParentEvent.IsZero() {
		ePrev := h.dagi.GetEvent(h.SelfParentEvent)
		selfIdx := h.validators.GetIdx(ePrev.Creator())
		if selfIdx == 0 {
			print = true
		}
	}
	if event.Frame() > h.eventTiming.maxKnownFrame {
		if print {
			fmt.Println("New maxframe: ", event.Frame(), " Old maxFrame: ", h.eventTiming.maxKnownFrame)
		}
		h.eventTiming.maxKnownFrame = event.Frame()        // record the maximum known frame
		h.FastOrSlow.newFrame(h.eventTiming.maxKnownFrame) // update performance measurement
	}
	creatorIdx := h.validators.GetIdx(event.Creator())
	if h.eventTiming.validatorHighestEvents[creatorIdx].event != nil {
		if event.Seq() > h.eventTiming.validatorHighestEvents[creatorIdx].event.Seq() { // check event occurs after existing event
			kNew := h.RootKnowledgeByCount(event.Frame(), event.ID(), nil)
			kPrev := h.eventTiming.validatorHighestEvents[creatorIdx].k
			h.eventTiming.validatorHighestEvents[creatorIdx].kChange = (kNew != kPrev)
			// store values for the new event
			h.eventTiming.validatorHighestEvents[creatorIdx].k = kNew
			h.eventTiming.validatorHighestEvents[creatorIdx].event = event
			h.eventTiming.validatorHighestEvents[creatorIdx].time = time

		}
	} else {
		kNew := h.RootKnowledgeByCount(event.Frame(), event.ID(), nil) // calculate the DAG progress of the new event
		kPrev := 0                                                     // no previous event is known
		h.eventTiming.validatorHighestEvents[creatorIdx].kChange = (kNew != kPrev)
		// store values for the new event
		h.eventTiming.validatorHighestEvents[creatorIdx].k = kNew
		h.eventTiming.validatorHighestEvents[creatorIdx].event = event
		h.eventTiming.validatorHighestEvents[creatorIdx].time = time
	}
	h.Performance()
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

	// now create list of candidates that have equal highest khat metric
	var bestCandidates hash.Events
	var bestMetrics []RootProgressMetrics
	maxMetric := metrics[0].khat
	for _, metric := range metrics {
		if metric.khat == maxMetric {
			bestCandidates = append(bestCandidates, candidateParents[metric.idx])
			bestMetrics = append(bestMetrics, metric)
		} else {
			break
		}
	}
	if len(bestCandidates) > 1 {
		// To choose from the list of equal highest khat metric canidates, sort them by all root knowledge metric, k
		metrics = h.GetMetricsOfRootKnowledge(bestCandidates, chosenParents, bestMetrics)
		sort.Sort(sortedRootProgressMetrics(metrics))
	}
	return metrics[0].idx
}

func (h *QuorumIndexer) GetMetricsOfRootKnowledge(candidateParents hash.Events, chosenParents hash.Events, metrics []RootProgressMetrics) []RootProgressMetrics {
	// This function is indended to be used in the process of
	// selecting parents from a set of candidate parents.
	// Candidate parents are assumed to be in the highest frame
	// This function returns a metric of root knowledge for assessing
	// the incremental increase in root knowledge when using each candidate head as a parent.
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

	// create a slice containing all chosen parents, and a candidate parent
	parents := make([]hash.Event, len(chosenParents)+1)
	for j, head := range chosenParents {
		parents[j] = head
	}
	for i, candidateParent := range candidateParents {
		parents[len(parents)-1] = candidateParent
		metrics[i].k = h.RootKnowledgeByCount(maxFrame, h.SelfParentEvent, parents)
	}

	return metrics
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
	tempMetric.khat = 0
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
		rootProgressMetrics[i].khat = h.progressTowardNewRoot(maxFrame, h.SelfParentEvent, parents)

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
	if m[i].khat != m[j].khat {
		return m[i].khat > m[j].khat
	} else {
		if m[i].k != m[j].k {
			return m[i].k > m[j].k
		} else {
			return m[i].stake > m[j].stake
		}
	}
}

func (h *QuorumIndexer) RootKnowledgeByStake(frame idx.Frame, event hash.Event, chosenHeads hash.Events) float64 {
	roots := h.store.GetFrameRoots(frame)
	Q := float64(h.validators.Quorum())
	D := (Q * Q)

	// calculate k for event under consideration

	RootKnowledge := make([]KIdx, len(roots))
	for i, root := range roots {
		rootValidatorIdx := h.validators.GetIdx(root.Slot.Validator)
		rootStake := h.validators.GetWeightByIdx(rootValidatorIdx)
		FCProgress, _ := h.dagi.ForklessCauseProgress(event, root.ID, nil, chosenHeads) //compute for new event
		if FCProgress.Sum() <= h.validators.Quorum() {
			RootKnowledge[i].K = float64(rootStake) * float64(FCProgress.Sum())
			// RootKnowledge[i].K = float64(FCProgress[0].Sum())
		} else {
			RootKnowledge[i].K = float64(rootStake) * float64(h.validators.Quorum())
			// RootKnowledge[i].K = float64(h.Validators.Quorum())
		}
		RootKnowledge[i].Root = root

	}

	sort.Sort(sortedKIdx(RootKnowledge))
	var kNew float64 = 0

	var bestRootsStake pos.Weight = 0
	for _, kidx := range RootKnowledge {
		rootValidatorIdx := h.validators.GetIdx(kidx.Root.Slot.Validator)
		rootStake := h.validators.GetWeightByIdx(rootValidatorIdx)
		if bestRootsStake >= h.validators.Quorum() {
			break
		} else if bestRootsStake+rootStake <= h.validators.Quorum() {
			// kNew += float64(kidx.K) * float64(rootStake)
			kNew += float64(kidx.K)
			bestRootsStake += rootStake
		} else {
			partialStake := h.validators.Quorum() - bestRootsStake
			kNew += float64(kidx.K) * float64(partialStake) / float64(rootStake)
			bestRootsStake = h.validators.Quorum() // this will trigger the break condition above
		}
	}
	kNew = kNew / D

	return kNew
}

func (m sortedKIdx) Len() int {
	return len(m)
}

func (m sortedKIdx) Swap(i, j int) {
	m[i], m[j] = m[j], m[i]
}

func (m sortedKIdx) Less(i, j int) bool {
	return m[i].K > m[j].K
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
	// threshold = 4
	return threshold
}

func (h *QuorumIndexer) classifyFastOrSlow() {
	var oldestFrameToCheck idx.Frame = 0
	var newestFrameToCheck idx.Frame = 0
	if h.eventTiming.maxKnownFrame > 2 {
		newestFrameToCheck = h.eventTiming.maxKnownFrame - 2
	} else {
		newestFrameToCheck = 0
	}
	if newestFrameToCheck > idx.Frame(h.eventTiming.numFramesToCheck) {
		oldestFrameToCheck = newestFrameToCheck - idx.Frame(h.eventTiming.numFramesToCheck)
	} else {
		oldestFrameToCheck = 0
	}

	// check which validators have produced a root in frames that have not yet been checked
	//+++in theory, even though we are at maxFrame, it is possible for a validator to produce a root in maxFrame-1 that is not yet known, especially if maxFrame is recently reached +++ TODO check from maxFrame-2?

	if h.eventTiming.newestFrameChecked > oldestFrameToCheck {
		oldestFrameToCheck = h.eventTiming.newestFrameChecked
	}
	if oldestFrameToCheck < 0 {
		oldestFrameToCheck = 0 //smallest frame number is zero, so don't try to check anything earlier +++TODO check 0 is the lowest possible frame number
	}
	index := (h.eventTiming.newestFrameIndex + 1) % h.eventTiming.numFramesToCheck
	for frame := oldestFrameToCheck; frame <= newestFrameToCheck; frame++ {
		h.eventTiming.newestFrameIndex = index //record index of circular buffer
		roots := h.store.GetFrameRoots(frame)
		// initilise all validators to no root i.e. false
		for valIdx := 0; valIdx < int(h.validators.Len()); valIdx++ {
			h.eventTiming.hasRoot[valIdx][index] = false
		}
		for _, root := range roots {
			valID := root.Slot.Validator
			valIdx := h.validators.GetIdx(valID)
			h.eventTiming.hasRoot[valIdx][index] = true
		}
		frame++
		index = (index + 1) % h.eventTiming.numFramesToCheck // increment circular buffer index, to overwrite old frame root information
	}
	h.eventTiming.newestFrameChecked = newestFrameToCheck // record newest frame checked

	// for each validator check that it has not missed producing a root in consecutive frames
	for valIdx := 0; valIdx < int(h.validators.Len()); valIdx++ {
		h.eventTiming.fast[valIdx] = true

		frameIdx := (h.eventTiming.newestFrameIndex + 1) % h.eventTiming.numFramesToCheck //start at the oldest frame
		rootInPrevFrame := true                                                           //no previous frame when starting, so take it to be true

		for numFrames := 0; numFrames < h.eventTiming.numFramesToCheck; numFrames++ {
			if !rootInPrevFrame && !h.eventTiming.hasRoot[valIdx][frameIdx] {
				h.eventTiming.fast[valIdx] = false // a validator is NOT fast if it has missed producing a root in consecutive frames
				break
			}
			rootInPrevFrame = h.eventTiming.hasRoot[valIdx][frameIdx]
			frameIdx = (frameIdx + 1) % h.eventTiming.numFramesToCheck // increment circular buffer index
		}
	}
}

func (h *QuorumIndexer) stakeThreshold() uint32 {
	// classify each validator as either fast or slow emitter
	// to be used to determine if self should be fast or slow
	// h.classifyFastOrSlow()

	// Cumulative count based threshold
	selfID := h.dagi.GetEvent(h.SelfParentEvent).Creator()
	selfStake := h.validators.GetWeightByIdx(h.validators.GetIdx(selfID))
	sortedWeights := h.validators.SortedWeights()
	sortedIDs := h.validators.SortedIDs()

	quorumValidator := int(h.validators.Len() - 1)
	checkForQuorum := true
	fastCounter := h.validators.NewCounter()
	for sortedIdx, ID := range sortedIDs {
		if selfID == ID {
			break
		}
		fastCounter.Count(ID)

		if checkForQuorum && fastCounter.HasQuorum() {
			quorumValidator = int(sortedIdx) // find the last validator required for quorum
			checkForQuorum = false
		}

	}

	var threshold uint32 = 0

	if float64(fastCounter.Sum()) < 0.79*float64(h.validators.TotalWeight()) {
		if selfStake < sortedWeights[quorumValidator] {
			threshold = uint32(float64(h.validators.Quorum()) * float64(sortedWeights[quorumValidator]) / float64(selfStake)) //give larger stake validators a smaller threshold so they produce events faster than smaller stake validators
		} else {
			threshold = uint32(float64(h.validators.Quorum()))
		}
	} else {
		threshold = uint32(h.validators.TotalWeight() + 1) //anything above total weight, so that events cant't be created according to this condition
	}
	// threshold = uint32(selfStake) + 1
	return threshold
}

func (h *QuorumIndexer) largestStakeThreshold() uint32 {
	selfID := h.dagi.GetEvent(h.SelfParentEvent).Creator()
	selfStake := h.validators.GetWeightByIdx(h.validators.GetIdx(selfID))
	sortedIDs := h.validators.SortedIDs()
	sortedWeights := h.validators.SortedWeights()
	fastCounter := h.validators.NewCounter()
	fast := false
	for _, ID := range sortedIDs {
		if ID == selfID {
			fast = true
			break
		}
		fastCounter.Count(ID)
		if float64(fastCounter.Sum()) > float64(h.validators.TotalWeight())*0.75 {
			break
		}
	}

	quorumValidator := 0
	quorumCounter := h.validators.NewCounter()
	for _, ID := range sortedIDs {
		quorumValidator++
		quorumCounter.Count(ID)
		if quorumCounter.HasQuorum() {
			break
		}

	}
	var threshold uint32 = 0
	if fast {
		threshold = uint32(float64(h.validators.Quorum()) * float64(sortedWeights[quorumValidator]) / float64(selfStake)) //give larger stake validators a smaller threshold so they produce events faster than smaller stake validators
	} else {
		threshold = uint32(float64(h.validators.TotalWeight()) + 1)
	}
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

func (h *QuorumIndexer) sigmoidFn(metric float64) float64 {
	fMetric := float64(metric) / float64(h.validators.TotalWeight())
	//centre := h.sigmoid.Centre
	centre := float64(h.validators.Quorum()) / float64(h.validators.TotalWeight())
	return 1.0 / (1.0 + math.Exp(-(fMetric-centre)/h.sigmoid.Slope))
}
func (h *QuorumIndexer) DAGProgressAndTimeIntervalEventTimingCondition(chosenHeads hash.Events, online map[idx.ValidatorID]bool, passedTime int) bool {

	ePrev := h.dagi.GetEvent(h.SelfParentEvent)
	selfFrame := ePrev.Frame()
	// selfIdx := h.validators.GetIdx(ePrev.Creator())

	var kGreaterCount uint32 = 0
	kGreaterStake := h.validators.NewCounter()
	kPrev := h.RootKnowledgeByCount(selfFrame, h.SelfParentEvent, nil) // calculate metric of root knowledge for previous self event
	// kNew := h.RootKnowledgeByCount(selfFrame, h.SelfParentEvent, chosenHeads) // calculate metric of root knowledge for new event under consideration

	// if kNew > kPrev { // this condition prevents the function always returning true when less than quorum nodes are online, and no DAG progress can be made
	for _, e := range h.eventTiming.validatorHighestEvents {
		if e.event != nil {
			eFrame := h.dagi.GetEvent(e.event.ID()).Frame()
			switch {
			case eFrame > selfFrame: // validator's frame is ahead of self
				kGreaterCount++
				kGreaterStake.Count(e.event.Creator())
				break
			// case time-h.validatorHighestEvents[selfIdx].time > timeThreshParam && time-h.validatorHighestEvents[i].time > timeThreshParam: // max time interval between receiveing events from this validator exceeded
			// 	kGreaterCount++
			// 	kGreaterStake.Count(e.event.Creator())
			// 	break
			// case h.validatorHighestEvents[i].kChange == false: // most recent event received from i did not increase k DAG progress metric
			// 	kGreaterCount++
			// 	kGreaterStake.Count(e.event.Creator())
			// 	break
			case eFrame == selfFrame:
				k := h.RootKnowledgeByCount(selfFrame, e.event.ID(), nil)
				if k >= kPrev {
					kGreaterCount++
					kGreaterStake.Count(e.event.Creator())
				}
			}
		}
	}

	// }
	sigmoidMetric := h.sigmoidFn(float64(kGreaterStake.Sum()))
	if sigmoidMetric*float64(passedTime) >= h.eventTiming.threshold {
		return true
	}
	return false
}

func (h *QuorumIndexer) DAGProgressEventTimingCondition(chosenHeads hash.Events, online map[idx.ValidatorID]bool, time uint32) (bool, int) {
	// This function is used to determine if a new event should be created based upon DAG progress.
	// Primarily the function works by ordering the highest known event from each validator in the DAG according to the metric returned by RootKnowledgeByCount.
	// If the number of validators exceeding the metric of the most recent self event is greater than a threshold count, a new event should be created.
	// To prevent event creation from stalling, validators can also be counted if no event has been received since the creation of the previous self event,
	// (e.g. if the validator is offline), or if the validator's most recent event does not improve upon its previous event, indicating a potential attempt to
	// prevent other validators from meeting the above event creation criteria.
	// timeThreshParam := 999999999 // +++TODO
	ePrev := h.dagi.GetEvent(h.SelfParentEvent)
	selfFrame := ePrev.Frame()
	// selfIdx := h.validators.GetIdx(ePrev.Creator())

	var kGreaterCount uint32 = 0
	kGreaterStake := h.validators.NewCounter()
	kPrev := h.RootKnowledgeByCount(selfFrame, h.SelfParentEvent, nil)        // calculate metric of root knowledge for previous self event
	kNew := h.RootKnowledgeByCount(selfFrame, h.SelfParentEvent, chosenHeads) // calculate metric of root knowledge for new event under consideration

	// kPrev := h.RootKnowledgeByStake(selfFrame, h.SelfParentEvent, nil)        // calculate metric of root knowledge for previous self event
	// kNew := h.RootKnowledgeByStake(selfFrame, h.SelfParentEvent, chosenHeads) // calculate metric of root knowledge for new event under consideration

	// if kNew > kPrev { // this condition prevents the function always returning true when less than quorum nodes are online, and no DAG progress can be made
	for _, e := range h.eventTiming.validatorHighestEvents {
		if e.event != nil {
			eFrame := h.dagi.GetEvent(e.event.ID()).Frame()
			switch {
			case eFrame > selfFrame: // validator's frame is ahead of self
				kGreaterCount++
				kGreaterStake.Count(e.event.Creator())
				break
			// case time-h.validatorHighestEvents[selfIdx].time > timeThreshParam && time-h.validatorHighestEvents[i].time > timeThreshParam: // max time interval between receiveing events from this validator exceeded
			// 	kGreaterCount++
			// 	kGreaterStake.Count(e.event.Creator())
			// 	break
			// case h.validatorHighestEvents[i].kChange == false: // most recent event received from i did not increase k DAG progress metric
			// 	kGreaterCount++
			// 	kGreaterStake.Count(e.event.Creator())
			// 	break
			case eFrame == selfFrame:
				k := h.RootKnowledgeByCount(selfFrame, e.event.ID(), nil)
				// k := h.RootKnowledgeByStake(selfFrame, e.event.ID(), nil)
				if k >= kPrev {
					kGreaterCount++
					kGreaterStake.Count(e.event.Creator())
				}
			}
		}
	}

	// if kGreaterCount >= h.Threshold() {
	// 	return true // self should create a new event
	// }
	// if uint32(kGreaterStake.Sum()) >= h.stakeThreshold() {
	// if uint32(kGreaterStake.Sum()) >= h.largestStakeThreshold() { // use thresholds according to stake only
	if uint32(kGreaterStake.Sum()) >= h.FastOrSlowThreshold() { // use threshold determined by validator performance
		// if kGreaterStake.Sum() >= h.validators.Quorum() {
		if kNew > kPrev {
			return true, kNew
		} else {

			// if time > 100 {
			// 	fmt.Println("!!!KNEW<=KPREV!!!")
			// 	return true, kNew
			// }
			// fmt.Println("!!!KNEW<=KPREV!!!No Event!!!")
		}
		// return true // self should create a new event
	}
	// }

	// }
	return false, kNew // self should not create a new event
}
