package abft

import (
	"crypto/sha256"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"github.com/Fantom-foundation/lachesis-base/emitter/ancestor"
	"github.com/Fantom-foundation/lachesis-base/hash"
	"github.com/Fantom-foundation/lachesis-base/inter/dag"
	"github.com/Fantom-foundation/lachesis-base/inter/dag/tdag"
	"github.com/Fantom-foundation/lachesis-base/inter/idx"
	"github.com/Fantom-foundation/lachesis-base/inter/pos"
	"github.com/Fantom-foundation/lachesis-base/kvdb"
	"github.com/Fantom-foundation/lachesis-base/kvdb/memorydb"
	"github.com/Fantom-foundation/lachesis-base/lachesis"
	"github.com/Fantom-foundation/lachesis-base/utils/adapters"
	"github.com/Fantom-foundation/lachesis-base/utils/piecefunc"
	"github.com/Fantom-foundation/lachesis-base/vecfc"
)

const (
	maxVal = math.MaxUint64/uint64(piecefunc.DecimalUnit) - 1
)

// TestLachesis extends Lachesis for tests.
type SimLachesis struct {
	*IndexedLachesis

	blocks      map[BlockKey]*BlockResult
	lastBlock   BlockKey
	epochBlocks map[idx.Epoch]idx.Frame

	applyBlock applyBlockFn

	confirmationTimer ConfirmationTimer
}

type emissionTimes struct {
	nowTime  int
	prevTime int
}

type Results struct {
	maxFrame  idx.Frame
	numEvents int
}

type QITestEvents []*QITestEvent

type QITestEvent struct {
	tdag.TestEvent
	creationTime     int
	confirmationTime int
	medianTime       int
}

type ConfirmationTimer struct {
	allEvents   []QITestEvent
	currentTime int
}

type NetworkGas struct {
	UseGas bool

	NetworkAllocPerMilliSecShort float64
	NetworkAllocPerMilliSecLong  float64
	MaxAllocPeriodShort          float64
	MaxAllocPeriodLong           float64

	StartupAllocPeriodLong  float64
	StartupAllocPeriodShort float64
	MinStartupGas           float64
	MinEnsuredAlloc         float64

	EventCreationGas float64
	MaxEventGas      float64
	ParentGas        float64
}

type ValidatorGas struct {
	AvailableLongGas               float64
	AvailableShortGas              float64
	ValidatorAllocPerMilliSecShort float64
	ValidatorAllocPerMilliSecLong  float64
	MaxLongGas                     float64
	MaxShortGas                    float64
}

var mutex sync.Mutex // a mutex used for variables shared across go rountines
var KOnly bool = false

// Configures a simulation of Lachesis consensus
func Benchmark_Emission(b *testing.B) {
	numNodes := 20
	stakeDist := stakeCumDist()             // for stakes drawn from distribution
	stakeRNG := rand.New(rand.NewSource(0)) // for stakes drawn from distribution

	weights := make([]pos.Weight, numNodes)
	for i, _ := range weights {
		// uncomment one of the below options for valiator stake distribution
		weights[i] = pos.Weight(sampleDist(stakeRNG, stakeDist)) // for non-equal stake sample from Fantom main net validator stake distribution
		// weights[i] = pos.Weight(1)                               //for equal stake
	}
	sort.Slice(weights, func(i, j int) bool { return weights[i] > weights[j] }) // sort weights in order
	FCParentCount := 10                                                         // maximum number of parents selected by FC indexer
	randParentCount := 0                                                        // maximum number of parents selected randomly
	offlineNodes := false                                                       // set to true to make smallest non-quourm validators offline

	// Gas setup
	var NetworkGas NetworkGas
	NetworkGas.UseGas = false                                                // set to true to simulate gas usage for event creation
	NetworkGas.MaxAllocPeriodLong = 60 * 60 * 1000                           // 60 minutes in units of milliseconds
	NetworkGas.MaxAllocPeriodShort = NetworkGas.MaxAllocPeriodLong / (2 * 6) // 5 minutes in units of milliseconds
	NetworkGas.EventCreationGas = 28000
	NetworkGas.NetworkAllocPerMilliSecLong = 100.0 / 1000.0 * NetworkGas.EventCreationGas
	NetworkGas.NetworkAllocPerMilliSecShort = 2 * NetworkGas.NetworkAllocPerMilliSecLong
	NetworkGas.MaxEventGas = 10000000 + NetworkGas.EventCreationGas
	NetworkGas.ParentGas = 2400
	NetworkGas.StartupAllocPeriodLong = 5000                                   // 5 seconds in units of milliseconds
	NetworkGas.StartupAllocPeriodShort = NetworkGas.StartupAllocPeriodLong / 2 // 5/2 seconds in units of milliseconds
	NetworkGas.MinStartupGas = 20 * NetworkGas.EventCreationGas
	NetworkGas.MinEnsuredAlloc = NetworkGas.MaxEventGas

	// Uncomment the desired latency type
	var simLatency latencyI
	var seed int64
	seed = 0 //use this for the same seed each time the simulator runs
	// seed = time.Now().UnixNano() //use this for a different seed each time the simulator runs

	// Latencies between validators are drawn from a Normal Gaussian distribution
	// var internetLatency gaussianLatency
	// internetLatency.mean = 50 // mean latency in milliseconds
	// internetLatency.std = 0   // standard deviation of latency in milliseconds
	// maxLatency := int(internetLatency.mean + 4*internetLatency.std)
	// fmt.Println("Gaussian Latency: seed: ", seed, " mean: ", internetLatency.mean, " std: ", internetLatency.std, " max latency: ", maxLatency)

	// Latencies between validators are modelled using a dataset of real world internet latencies between cities
	// var internetLatency cityLatency
	// fmt.Println("City Latency: seed: ", seed)
	// maxLatency := internetLatency.initialise(numNodes, seed)

	// Latencies between validators are drawn from a dataset of latencies observed by one Fantom main net validator. Note all pairs of validators will use the same distribution
	var internetLatency mainNetLatency
	maxLatency := internetLatency.initialise()

	// Overlay a P2P network between validators on top of internet
	var P2P P2P
	P2P.useP2P = false
	if P2P.useP2P {
		P2P.numValidators = numNodes
		P2P.randSrc = rand.New(rand.NewSource(seed)) // seed RNG for selecting peers
		P2P.maxPeers = 10
		P2P.internetLatencies = &internetLatency
		P2P.randomSymmetricPeers(false)
		P2P.calculateP2PLatencies()
		maxLatency = P2P.maxLatency
		simLatency = P2P.P2PLatencies
	} else {
		simLatency = internetLatency
	}
	//Print latencies between validators through the P2P network
	tempRandSrc := rand.New(rand.NewSource(seed))
	fmt.Print("meanLatencies=np.array([")
	for i := 0; i < numNodes; i++ {
		fmt.Print("[")
		for j := 0; j < numNodes; j++ {
			fmt.Print(int(simLatency.latency(i, j, tempRandSrc)), ",")
		}
		fmt.Println("],")
	}
	fmt.Println("])")
	fmt.Println("")
	fmt.Println("Max Latency: ", maxLatency)
	simulationDuration := 50000 // length of simulated time in milliseconds

	UseFCNotQI := true // uses FC parent selection and event timing
	// UseFCNotQI := false //uses QI parent selection and event timing
	// Now run the simulation
	KOnly = true
	simulate(weights, FCParentCount, randParentCount, offlineNodes, &simLatency, maxLatency, simulationDuration, UseFCNotQI, NetworkGas)
	KOnly = false
	simulate(weights, FCParentCount, randParentCount, offlineNodes, &simLatency, maxLatency, simulationDuration, UseFCNotQI, NetworkGas)
	UseFCNotQI = false //uses QI parent selection and event timing
	simulate(weights, FCParentCount, randParentCount, offlineNodes, &simLatency, maxLatency, simulationDuration, UseFCNotQI, NetworkGas)
}

func simulate(weights []pos.Weight, FCQIParentCount int, randParentCount int, offlineNodes bool, latency *latencyI, maxLatency int, simulationDuration int, UseFCNotQI bool, networkGas NetworkGas) Results {
	// This function simulates the operation of a distributed network of validators. The simulation can be used to develop and test methods for improving Lachesis consensus.
	numValidators := len(weights)

	randSrc := rand.New(rand.NewSource(0)) // use a fixed seed of 0 for comparison between runs

	latencyRNG := make([]*rand.Rand, numValidators)
	randParentRNG := make([]*rand.Rand, numValidators)
	randEvRNG := make([]*rand.Rand, numValidators)
	for i := range weights {
		// Use same seed each time the simulator is used
		latencyRNG[i] = rand.New(rand.NewSource(0))
		randParentRNG[i] = rand.New(rand.NewSource(0))
		randEvRNG[i] = rand.New(rand.NewSource(0))

		// Uncomment to use a different seed each time the simulator is used
		// time.Sleep(1 * time.Millisecond) //sleep a bit for seeding RNG
		// latencyRNG[i] = rand.New(rand.NewSource(time.Now().UnixNano()))
		// time.Sleep(1 * time.Millisecond) //sleep a bit for seeding RNG
		// randParentRNG[i] = rand.New(rand.NewSource(time.Now().UnixNano()))
		// time.Sleep(1 * time.Millisecond) //sleep a bit for seeding RNG
		// randEvRNG[i] = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	randEvRate := 0.0 // sets the probability that an event will be created randomly

	// create a 3D slice with coordinates [time][node][node] that is used to store delayed transmission of events between nodes
	//each time coordinate corresponds to 1 millisecond of delay between a pair of nodes
	eventPropagation := make([][][][]*QITestEvent, maxLatency)
	for i := range eventPropagation {
		eventPropagation[i] = make([][][]*QITestEvent, numValidators)
		for j := range eventPropagation[i] {
			eventPropagation[i][j] = make([][]*QITestEvent, numValidators)
			for k := range eventPropagation[i][j] {
				eventPropagation[i][j][k] = make([]*QITestEvent, 0)
			}
		}
	}

	// create a list of heads for each node
	headsAll := make([]dag.Events, numValidators)

	//setup nodes
	nodes := tdag.GenNodes(numValidators)
	validators := pos.ArrayToValidators(nodes, weights)
	inputs := make([]EventStore, numValidators)
	lchs := make([]*SimLachesis, numValidators)

	fcIndexers := make([]*ancestor.FCIndexer, numValidators)
	qiIndexers := make([]*ancestor.QuorumIndexer, numValidators)

	diffMetricFn := func(median, current, update idx.Event, validatorIdx idx.Validator) ancestor.Metric {
		return updMetric(median, current, update, validatorIdx, validators)
	}

	spammer := make([]bool, numValidators)
	for i := 0; i < numValidators; i++ {
		lch, _, input, dagIndexer := SimulatorLachesis(nodes, weights)
		lchs[i] = lch
		inputs[i] = *input
		// lchs[i].confirmationTimer = &confirmationTimers[i]
		spammer[i] = false
		// if i == 1 {
		// 	spammer[i] = true
		// }
		if UseFCNotQI {
			fcIndexers[i] = ancestor.NewFCIndexer(validators, dagIndexer, input.GetEvent, nodes[i], spammer[i])
		} else {
			qiIndexers[i] = ancestor.NewQuorumIndexer(validators, lch.dagIndex, diffMetricFn)
		}

	}

	// If requried set smallest non-quorum validators as offline for testing
	sortWeights := validators.SortedWeights()
	sortedIDs := validators.SortedIDs()
	onlineStake := validators.TotalWeight()
	online := make(map[idx.ValidatorID]bool)
	for i := len(sortWeights) - 1; i >= 0; i-- {
		online[sortedIDs[i]] = true
		if offlineNodes {
			if float64(onlineStake-sortWeights[i]) >= 0.67*float64(validators.TotalWeight()) { //validators.Quorum() {
				onlineStake -= sortWeights[i]
				online[sortedIDs[i]] = false
			}
		}
	}

	ValidatorGas := make([]ValidatorGas, numValidators)
	for i, weight := range weights {

		weightFrac := float64(weight) / float64(validators.TotalWeight())
		ValidatorGas[i].ValidatorAllocPerMilliSecLong = networkGas.NetworkAllocPerMilliSecLong * weightFrac
		ValidatorGas[i].MaxLongGas = networkGas.MaxAllocPeriodLong * ValidatorGas[i].ValidatorAllocPerMilliSecLong
		if ValidatorGas[i].MaxLongGas < networkGas.MinEnsuredAlloc {
			ValidatorGas[i].MaxLongGas = networkGas.MinEnsuredAlloc
		}
		ValidatorGas[i].ValidatorAllocPerMilliSecShort = networkGas.NetworkAllocPerMilliSecShort * weightFrac
		ValidatorGas[i].MaxShortGas = networkGas.NetworkAllocPerMilliSecShort * ValidatorGas[i].ValidatorAllocPerMilliSecShort
		if ValidatorGas[i].MaxShortGas < networkGas.MinEnsuredAlloc {
			ValidatorGas[i].MaxShortGas = networkGas.MinEnsuredAlloc
		}
		// set storage levels of startup gas
		ValidatorGas[i].AvailableLongGas = ValidatorGas[i].ValidatorAllocPerMilliSecLong * networkGas.StartupAllocPeriodLong
		if ValidatorGas[i].AvailableLongGas < networkGas.MinStartupGas {
			ValidatorGas[i].AvailableLongGas = networkGas.MinStartupGas
		}
		ValidatorGas[i].AvailableShortGas = ValidatorGas[i].ValidatorAllocPerMilliSecShort * networkGas.StartupAllocPeriodShort
		if ValidatorGas[i].AvailableShortGas < networkGas.MinStartupGas {
			ValidatorGas[i].AvailableShortGas = networkGas.MinStartupGas
		}
	}

	stakeRatios := stakeRatios(*validators, online)
	minCheckInterval := 11 // min interval before re-checking if event can be created
	prevCheckTime := make([]int, numValidators)
	minEventCreationInterval := make([]int, numValidators) // minimum interval between creating event
	for i, _ := range minEventCreationInterval {
		minEventCreationInterval[i] = 11
	}
	// initial delay to avoid synchronous events
	initialDelay := make([]int, numValidators)
	for i := range initialDelay {
		initialDelay[i] = randSrc.Intn(100) // all validators should produce their leaf events within 100 ms from the start of the simulation
	}

	bufferedEvents := make([]QITestEvents, numValidators)

	eventsComplete := make([]int, numValidators)
	// setup flag to indicate leaf event
	isLeaf := make([]bool, numValidators)
	for node := range isLeaf {
		isLeaf[node] = true
	}

	selfParent := make([]QITestEvent, numValidators)

	wg := sync.WaitGroup{} // used for parallel go routines

	timeIdx := maxLatency - 1 // circular buffer time index
	simTime := -1             // counts simulated time

	// now start the simulation
	for simTime < simulationDuration {
		// move forward one timestep
		timeIdx = (timeIdx + 1) % maxLatency
		simTime = simTime + 1
		for i, _ := range lchs {
			lchs[i].confirmationTimer.currentTime = simTime
		}
		if simTime%1000 == 0 {
			fmt.Print(" TIME: ", simTime) // print time progress for tracking simulation progression
		}

		// Check to see if new events are received by nodes
		// if they are, do the appropriate updates for the received event
		for receiveNode := 0; receiveNode < numValidators; receiveNode++ {
			wg.Add(1)
			go func(receiveNode int) {
				defer wg.Done()
				// check for events to be received by other nodes (including self)
				for sendNode := 0; sendNode < numValidators; sendNode++ {
					mutex.Lock()
					for i := 0; i < len(eventPropagation[timeIdx][sendNode][receiveNode]); i++ {
						e := eventPropagation[timeIdx][sendNode][receiveNode][i]
						//add new event to buffer for cheecking if events are ready to put in DAG
						bufferedEvents[receiveNode] = append(bufferedEvents[receiveNode], e)
					}
					//clear the events at this time index

					eventPropagation[timeIdx][sendNode][receiveNode] = eventPropagation[timeIdx][sendNode][receiveNode][:0]
					mutex.Unlock()
				}
				// it is required that all of an event's parents have been received before adding to DAG
				// loop through buffer to check for events that can be processed
				process := make([]bool, len(bufferedEvents[receiveNode]))
				for i, buffEvent := range bufferedEvents[receiveNode] {
					process[i] = true
					//check if all parents are in the DAG
					for _, parent := range buffEvent.Parents() {
						if lchs[receiveNode].input.GetEvent(parent) == nil {
							// a parent is not yet in the DAG, so don't process this event yet
							process[i] = false
							break
						}
					}
					if process[i] {
						// buffered event has all parents in the DAG and can now be processed
						mutex.Lock()
						processEvent(inputs[receiveNode], lchs[receiveNode], buffEvent, fcIndexers[receiveNode], qiIndexers[receiveNode], &headsAll[receiveNode], nodes[receiveNode], simTime, UseFCNotQI)
						// check for different Atropos
						var maxKey BlockKey
						for key, _ := range lchs[receiveNode].blocks {
							if key.Frame > maxKey.Frame {
								maxKey = key
							}
						}
						recVal := lchs[receiveNode].blocks[maxKey]
						for j, _ := range validators.IDs() {
							if _, ok := lchs[j].blocks[maxKey]; ok {
								if lchs[j].blocks[maxKey].Atropos != recVal.Atropos {
									fmt.Println("Error: Different Atropos at frame: ", maxKey.Frame, " Validator: ", receiveNode, "Validator: ", j)
								}
							}
						}
						mutex.Unlock()
					}
				}
				//remove processed events from buffer
				temp := make([]*QITestEvent, len(bufferedEvents[receiveNode]))
				copy(temp, bufferedEvents[receiveNode])
				bufferedEvents[receiveNode] = bufferedEvents[receiveNode][:0] //clear buffer
				for i, processed := range process {
					if processed == false {
						bufferedEvents[receiveNode] = append(bufferedEvents[receiveNode], temp[i]) // put unprocessed event back in the buffer
					}

				}
			}(receiveNode)
		}
		wg.Wait()

		// Build events and check timing condition
		for self := 0; self < numValidators; self++ {
			passedTime := simTime - prevCheckTime[self] // time since creating previous event
			if passedTime >= minCheckInterval {
				prevCheckTime[self] = simTime
				// self is ready to try creating a new event
				wg.Add(1)
				go func(self int) { //parallel
					defer wg.Done()

					if initialDelay[self] > 0 {
						// don't create an event during an initial delay in creating the first event at the start of the simulation
						initialDelay[self]--
					} else {
						//create the event datastructure
						selfID := nodes[self]
						e := &QITestEvent{}
						e.SetCreator(selfID)
						e.SetParents(hash.Events{}) // first parent is empty hash

						var parents dag.Events
						if isLeaf[self] { // leaf event
							e.SetSeq(1)
							e.SetLamport(1)
						} else { // normal event
							e.SetSeq(selfParent[self].Seq() + 1)
							e.SetLamport(selfParent[self].Lamport() + 1)
							parents = append(parents, &selfParent[self].BaseEvent) // always use self's previous event as a parent
						}

						// get heads for parent selection
						var heads dag.Events
						var allHeads dag.Events
						for _, head := range headsAll[self] {
							heads = append(heads, head)
							allHeads = append(allHeads, head)
						}
						for i, head := range heads {
							if selfParent[self].BaseEvent.ID() == head.ID() {
								// remove the self parent from options, it is already a parent
								heads[i] = heads[len(heads)-1]
								heads = heads[:len(heads)-1]
								break
							}
						}

						if !isLeaf[self] { // only non leaf events have parents
							// iteratively select the best parent from the list of heads using quorum indexer parent selection
							for j := 0; j < FCQIParentCount-1; j++ {
								if len(heads) <= 0 {
									//no more heads to choose, adding more parents will not improve DAG progress
									break
								}
								var best int
								if UseFCNotQI {
									best = fcIndexers[self].SearchStrategy().Choose(parents.IDs(), heads.IDs())

								} else {
									best = qiIndexers[self].SearchStrategy().Choose(parents.IDs(), heads.IDs())
								}

								parents = append(parents, heads[best])
								// remove chosen parent from head options
								heads[best] = heads[len(heads)-1]
								heads = heads[:len(heads)-1]
								if spammer[self] {
									heads = heads[:0] // spammer validator only chooses one parent
								}
							}

							// now select random parents
							for j := 0; j < randParentCount-1; j++ {
								if len(heads) <= 0 {
									//no more heads to choose, adding more parents will not improve DAG progress
									break
								}
								randParent := randParentRNG[self].Intn(len(heads))
								parents = append(parents, heads[randParent])
								// remove chosen parent from head options
								heads[randParent] = heads[len(heads)-1]
								heads = heads[:len(heads)-1]
							}

							// parent selection is complete, add selected parents to new event
							for _, parent := range parents {
								e.AddParent(parent.ID())
								if e.Lamport() <= parent.Lamport() {
									e.SetLamport(parent.Lamport() + 1)
								}
							}
						}
						// name and ID the event
						e.SetEpoch(1) // use epoch 1 for simulation
						e.Name = fmt.Sprintf("%03d%04d", self, e.Seq())
						hasher := sha256.New()
						hasher.Write(e.Bytes())
						var id [24]byte
						copy(id[:], hasher.Sum(nil)[:24])
						e.SetID(id)
						hash.SetEventName(e.ID(), fmt.Sprintf("%03d%04d", self, e.Seq()))
						e.creationTime = simTime
						var times emissionTimes
						times.nowTime = simTime
						times.prevTime = selfParent[self].creationTime

						createRandEvent := randEvRNG[self].Float64() < randEvRate // used for introducing randomly created events
						if online[selfID] == true {
							// self is online
							passedTime := simTime - selfParent[self].creationTime
							if passedTime > minEventCreationInterval[self] {
								var pastMe pos.Weight
								if UseFCNotQI {
									pastMe = fcIndexers[self].ValidatorsPastMe()
								}
								gasOK := true
								gasUsed := 0.0
								if networkGas.UseGas {
									// configfure below to simulate gas usage
									SetEventMedianTime(e, lchs[self], validators)
									gasUsed = networkGas.EventCreationGas + float64(len(e.Parents()))*networkGas.ParentGas
									// Add in any further gas usage here for txs etc
									// gasUsed+=???
									gasOK = sufficientGas(e, lchs[self], validators, &ValidatorGas[self], gasUsed)
								} else {
									gasOK = true //sufficientGas(e, &lchs[self], &quorumIndexers[self], &ValidatorGas[self], gasUsed)
								}
								if gasOK {
									if createRandEvent || isLeaf[self] || readyToEmit(UseFCNotQI, validators, times, pastMe, fcIndexers[self], qiIndexers[self], e, stakeRatios[e.Creator()], spammer[self]) {
										//create an event if (i)a random event is created (ii) is a leaf event, or (iii) event timing condition is met
										isLeaf[self] = false // only create one leaf event
										if networkGas.UseGas {
											updateGas(e, lchs[self], &ValidatorGas[self], gasUsed)
										}
										//now start propagation of event to other nodes
										delay := 1
										for receiveNode := 0; receiveNode < numValidators; receiveNode++ {
											if receiveNode == self {
												delay = 1 // no delay to send to self (self will 'receive' its own event after time increment at the top of the main loop)
											} else {
												delay = int((*latency).latency(self, receiveNode, latencyRNG[self]))
												// check delay is within min and max bounds
												if delay < 1 {
													delay = 1
												}
												if delay >= maxLatency {
													delay = maxLatency - 1
												}
											}
											receiveTime := (timeIdx + delay) % maxLatency // time index for the circular buffer
											mutex.Lock()
											eventPropagation[receiveTime][self][receiveNode] = append(eventPropagation[receiveTime][self][receiveNode], e) // add the event to the buffer
											mutex.Unlock()
										}
										eventsComplete[self]++ // increment count of events created for this node
										selfParent[self] = *e  //update self parent to be this new event
										mutex.Lock()
										for i, _ := range lchs {
											lchs[i].AddConfirmationTimerEvent(e)
										}
										mutex.Unlock()
									}
								}
							}
						}
					}
				}(self)
			}
		}
		wg.Wait()

	}

	// print some useful output
	fmt.Println("")
	fmt.Println("Simulated time ", float64(simTime)/1000.0, " seconds")
	fmt.Println("Number of nodes: ", numValidators)
	numOnlineNodes := 0
	for _, isOnline := range online {
		if isOnline {
			numOnlineNodes++
		}
	}
	fmt.Println("Number of nodes online: ", numOnlineNodes)
	if UseFCNotQI {
		fmt.Println("Using FC indexer, KOnly", KOnly)
	} else {
		fmt.Println("Using quorum indexer")
	}
	fmt.Println("Max Total Parents: ", FCQIParentCount+randParentCount, " Max FC/QI Parents:", FCQIParentCount, " Max Random Parents", randParentCount)

	// print number of events created by each node
	var totalEventsComplete int = 0
	for i, nEv := range eventsComplete {
		totalEventsComplete += nEv
		fmt.Println("Stake: ", weights[i], "event rate: ", float64(nEv)*1000/float64(simTime), " events/stake: ", float64(nEv)/float64(weights[i]))
	}
	var maxFrame idx.Frame = 0
	for _, events := range headsAll {
		for _, event := range events {
			if event.Frame() > maxFrame {
				maxFrame = event.Frame()
			}
		}
	}

	fmt.Println(" Number of Events: ", totalEventsComplete)
	fmt.Println("Event rate per (online) node: ", float64(totalEventsComplete)/float64(numOnlineNodes)/(float64(simTime)/1000.0))
	fmt.Println("Max Frame: ", maxFrame)
	fmt.Println("[Indicator of TTF] Frames per second: ", (1000.0*float64(maxFrame))/float64(simTime))
	fmt.Println("[Indictor of efficiency] Average events per frame per (online) node: ", (float64(totalEventsComplete))/(float64(maxFrame)*float64(numOnlineNodes)))

	// now calculate TTF from event confirmation times
	minTTF := simulationDuration
	maxTTF := 0
	meanTTF := 0
	meanCtr := 0
	maxCreationTime := 0
	for _, lch := range lchs {
		for _, e := range lch.confirmationTimer.allEvents {
			if maxCreationTime < e.creationTime {
				maxCreationTime = e.creationTime
			}
			if e.confirmationTime > 0 { //only use confirmed events in finalised blocks
				TTF := e.confirmationTime - e.creationTime
				meanTTF += TTF
				meanCtr++
				if TTF < minTTF {
					minTTF = TTF
				}
				if TTF > maxTTF {
					maxTTF = TTF
				}
			}
		}
	}
	if meanCtr > 0 {
		meanTTF = meanTTF / meanCtr
	}

	fmt.Println("Event mean TTF: ", meanTTF, " ms, min TTF: ", minTTF, " ms, max TTF: ", maxTTF, " ms")
	fmt.Println("Last event created at: ", maxCreationTime, ", simulation ended at: ", simulationDuration, ". If this is not near the end of the simulation, the DAG may have stalled due to lack of gas, online nodes, non-forking nodes, etc.")
	var results Results
	results.maxFrame = maxFrame
	results.numEvents = totalEventsComplete
	return results

}

func (lch *SimLachesis) AddConfirmationTimerEvent(event *QITestEvent) {
	lch.confirmationTimer.allEvents = append(lch.confirmationTimer.allEvents, *event)
}

func (lch *SimLachesis) ApplyEvent(event dag.Event) {
	for i := len(lch.confirmationTimer.allEvents) - 1; i >= 0; i-- {
		if lch.confirmationTimer.allEvents[i].ID() == event.ID() {
			lch.confirmationTimer.allEvents[i].confirmationTime = lch.confirmationTimer.currentTime
		}
	}
}

func updateHeads(newEvent dag.Event, heads *dag.Events) {
	// remove newEvent's parents from heads
	for _, parent := range newEvent.Parents() {
		for i := 0; i < len(*heads); i++ {
			if (*heads)[i].ID() == parent {
				(*heads)[i] = (*heads)[len(*heads)-1]
				*heads = (*heads)[:len(*heads)-1]
				// break
			}
		}
	}
	*heads = append(*heads, newEvent) //add newEvent to heads
}

func processEvent(input EventStore, lchs *SimLachesis, e *QITestEvent, fcIndexer *ancestor.FCIndexer, qiIndexer *ancestor.QuorumIndexer, heads *dag.Events, self idx.ValidatorID, time int, FCNotQI bool) (frame idx.Frame) {
	input.SetEvent(e)

	lchs.dagIndexer.Add(e)
	lchs.Lachesis.Build(e)
	lchs.Lachesis.Process(e)

	lchs.dagIndexer.Flush()
	if FCNotQI {
		// HighestBefore based fc indexer needs to process the event
		if e.SelfParent() == nil {
			fcIndexer.ProcessEvent(&e.BaseEvent, nil)
		} else {
			fcIndexer.ProcessEvent(&e.BaseEvent, input.GetEvent(*e.SelfParent()))
		}

	} else {
		qiIndexer.ProcessEvent(&e.BaseEvent, e.Creator() == self)
	}

	updateHeads(e, heads)
	return e.Frame()
}

func stakeRatios(validators pos.Validators, onlineValidators map[idx.ValidatorID]bool) map[idx.ValidatorID]uint64 {
	stakeRatio := make(map[idx.ValidatorID]uint64)
	totalStakeBefore := pos.Weight(0)
	for i, stake := range validators.SortedWeights() {
		vid := validators.GetID(idx.Validator(i))
		// pos.Weight is uint32, so cast to uint64 to avoid an overflow
		stakeRatio[vid] = uint64(totalStakeBefore) * uint64(piecefunc.DecimalUnit) / uint64(validators.TotalWeight())
		if onlineValidators[vid] {
			totalStakeBefore += stake
		}
	}
	return stakeRatio
}

func isAllowedToEmit(passedTime int, stakeRatio uint64, metric ancestor.Metric) bool {
	// This function recreates the relevant parts of isAllowedToEmit in go-opera
	passedTimeIdle := 0 // transactions are not simulated, assume the network is busy and therefore set this to zero
	if stakeRatio < 0.35*piecefunc.DecimalUnit {
		// top validators emit event right after transaction is originated
		passedTimeIdle = passedTime
	} else if stakeRatio < 0.7*piecefunc.DecimalUnit {
		// top validators emit event right after transaction is originated
		passedTimeIdle = (passedTimeIdle + passedTime) / 2
	}
	if passedTimeIdle > passedTime {
		passedTimeIdle = passedTime
	}
	adjustedPassedTime := (ancestor.Metric(passedTime) * metric) / piecefunc.DecimalUnit
	// adjustedPassedIdleTime := time.Duration(ancestor.Metric(passedTimeIdle/piecefunc.DecimalUnit) * metric)

	minInterval := 110 //emitter time interval used in go-opera
	if passedTime < minInterval {
		return false
	}
	if adjustedPassedTime < ancestor.Metric(minInterval) {
		return false
	}
	return true

}

// Approximates go-opera conditions for event emission
func readyToEmit(FCNotQI bool, validators *pos.Validators, times emissionTimes, pastMe pos.Weight, fcIndexer *ancestor.FCIndexer, qiIndexer *ancestor.QuorumIndexer, e dag.Event, stakeRatio uint64, spammer bool) (ready bool) {
	var metric ancestor.Metric

	if FCNotQI {
		metric = (ancestor.Metric(pastMe) * piecefunc.DecimalUnit) / ancestor.Metric(validators.TotalWeight())

		// if pastMe < thresh {
		if pastMe < validators.Quorum() {
			metric /= 15
		}
		if metric < 0.03*piecefunc.DecimalUnit {
			metric = 0.03 * piecefunc.DecimalUnit
		}
		// prefer new events increase root knowledge by reducing metric of events that don't increase root knowledge
		if !fcIndexer.RootKnowledgeIncrease(e.Parents()) {
			metric /= 15
		}
		metric = overheadAdjustedEventMetricF(validators.Len(), uint64(1*piecefunc.DecimalUnit), metric) // busyRate assumed to be 1

	} else {
		metric = eventMetric(qiIndexer.GetMetricOf(e.Parents()), e.Seq())
		metric = overheadAdjustedEventMetricF(validators.Len(), uint64(1*piecefunc.DecimalUnit), metric) // busyRate assumed to be 1
	}
	if !FCNotQI || !KOnly {
		passedTime := times.nowTime - times.prevTime
		return isAllowedToEmit(passedTime, stakeRatio, metric)
	} else {
		if spammer {
			return fcIndexer.RootKnowledgeIncrease(e.Parents()) && fcIndexer.HighestBeforeRootKnowledgeIncrease(*e.SelfParent(), e.Parents()).HasQuorum() // spammer validator will try to spam events by emitting whenever it can increase DAG progress metric
		}
		// return pastMe >= validators.Quorum() && fcIndexer.RootKnowledgeIncrease(e.Parents()) && fcIndexer.HighestBeforeRootKnowledgeIncrease(*e.SelfParent(), e.Parents())
		// return fcIndexer.HighestBeforeSeqIncrease(*e.SelfParent(), e.Parents()).HasQuorum() && fcIndexer.RootKnowledgeIncrease(e.Parents())
		return fcIndexer.HighestBeforeRootKnowledgeIncreaseAboveSelfParent(*e.SelfParent(), e.Parents()).HasQuorum() && fcIndexer.RootKnowledgeIncrease(e.Parents())
	}

}

func scalarUpdMetric(diff idx.Event, weight pos.Weight, totalWeight pos.Weight) ancestor.Metric {
	scalarUpdMetricF := piecefunc.NewFunc([]piecefunc.Dot{
		{
			X: 0,
			Y: 0,
		},
		{ // first observed event gives a major metric diff
			X: 1.0 * piecefunc.DecimalUnit,
			Y: 0.66 * piecefunc.DecimalUnit,
		},
		{ // second observed event gives a minor diff
			X: 2.0 * piecefunc.DecimalUnit,
			Y: 0.8 * piecefunc.DecimalUnit,
		},
		{ // other observed event give only a subtle diff
			X: 8.0 * piecefunc.DecimalUnit,
			Y: 0.99 * piecefunc.DecimalUnit,
		},
		{
			X: 100.0 * piecefunc.DecimalUnit,
			Y: 0.999 * piecefunc.DecimalUnit,
		},
		{
			X: 10000.0 * piecefunc.DecimalUnit,
			Y: 0.9999 * piecefunc.DecimalUnit,
		},
	})
	return ancestor.Metric(scalarUpdMetricF(uint64(diff)*piecefunc.DecimalUnit)) * ancestor.Metric(weight) / ancestor.Metric(totalWeight)
}

func updMetric(median, cur, upd idx.Event, validatorIdx idx.Validator, validators *pos.Validators) ancestor.Metric {
	if upd <= median || upd <= cur {
		return 0
	}
	weight := validators.GetWeightByIdx(validatorIdx)
	if median < cur {
		return scalarUpdMetric(upd-median, weight, validators.TotalWeight()) - scalarUpdMetric(cur-median, weight, validators.TotalWeight())
	}
	return scalarUpdMetric(upd-median, weight, validators.TotalWeight())
}

func overheadAdjustedEventMetricF(validatorsNum idx.Validator, busyRate uint64, eventMetric ancestor.Metric) ancestor.Metric {
	return ancestor.Metric(piecefunc.DecimalUnit-overheadF(validatorsNum, busyRate)) * eventMetric / piecefunc.DecimalUnit
}

func overheadF(validatorsNum idx.Validator, busyRate uint64) uint64 {
	validatorsToOverheadF := piecefunc.NewFunc([]piecefunc.Dot{
		{
			X: 0,
			Y: 0,
		},
		{
			X: 25,
			Y: 0.05 * piecefunc.DecimalUnit,
		},
		{
			X: 50,
			Y: 0.2 * piecefunc.DecimalUnit,
		},
		{
			X: 100,
			Y: 0.7 * piecefunc.DecimalUnit,
		},
		{
			X: 200,
			Y: 0.9 * piecefunc.DecimalUnit,
		},
		{
			X: 1000,
			Y: 1.0 * piecefunc.DecimalUnit,
		},
	})
	if busyRate > piecefunc.DecimalUnit {
		busyRate = piecefunc.DecimalUnit
	}
	return validatorsToOverheadF(uint64(validatorsNum)) * busyRate / piecefunc.DecimalUnit
}

func eventMetric(orig ancestor.Metric, seq idx.Event) ancestor.Metric {
	// eventMetricF is a piecewise function for event metric adjustment depending on a non-adjusted event metric
	eventMetricF := piecefunc.NewFunc([]piecefunc.Dot{
		{ // event metric is never zero
			X: 0,
			Y: 0.005 * piecefunc.DecimalUnit,
		},
		{
			X: 0.01 * piecefunc.DecimalUnit,
			Y: 0.03 * piecefunc.DecimalUnit,
		},
		{ // if metric is below ~0.2, then validator shouldn't emit event unless waited very long
			X: 0.2 * piecefunc.DecimalUnit,
			Y: 0.05 * piecefunc.DecimalUnit,
		},
		{
			X: 0.3 * piecefunc.DecimalUnit,
			Y: 0.22 * piecefunc.DecimalUnit,
		},
		{ // ~0.3-0.5 is an optimal metric to emit an event
			X: 0.4 * piecefunc.DecimalUnit,
			Y: 0.45 * piecefunc.DecimalUnit,
		},
		{
			X: 1.0 * piecefunc.DecimalUnit,
			Y: 1.0 * piecefunc.DecimalUnit,
		},
	})
	metric := ancestor.Metric(eventMetricF(uint64(orig)))
	// kick start metric in a beginning of epoch, when there's nothing to observe yet
	if seq <= 2 && metric < 0.9*piecefunc.DecimalUnit {
		metric += 0.1 * piecefunc.DecimalUnit
	}
	if seq <= 1 && metric <= 0.8*piecefunc.DecimalUnit {
		metric += 0.2 * piecefunc.DecimalUnit
	}
	return metric
}

func NewFunc(dots []Dot) func(x uint64) uint64 {
	if len(dots) < 2 {
		panic("too few dots")
	}

	var prevX uint64
	for i, dot := range dots {
		if i >= 1 && dot.X <= prevX {
			panic("non monotonic X")
		}
		if dot.Y > maxVal {
			panic("too large Y")
		}
		if dot.X > maxVal {
			panic("too large X")
		}
		prevX = dot.X
	}

	return Func{
		dots: dots,
	}.Get
}

// Dot is a pair of numbers
type Dot struct {
	X uint64
	Y uint64
}

type Func struct {
	dots []Dot
}

// Mul is multiplication of ratios with integer numbers
func Mul(a, b uint64) uint64 {
	return a * b / piecefunc.DecimalUnit
}

// Div is division of ratios with integer numbers
func Div(a, b uint64) uint64 {
	return a * piecefunc.DecimalUnit / b
}

// Get calculates f(x), where f is a piecewise linear function defined by the pieces
func (f Func) Get(x uint64) uint64 {
	if x < f.dots[0].X {
		return f.dots[0].Y
	}
	if x > f.dots[len(f.dots)-1].X {
		return f.dots[len(f.dots)-1].Y
	}
	// find a piece
	p0 := len(f.dots) - 2
	for i, piece := range f.dots {
		if i >= 1 && i < len(f.dots)-1 && piece.X > x {
			p0 = i - 1
			break
		}
	}
	// linearly interpolate
	p1 := p0 + 1

	x0, x1 := f.dots[p0].X, f.dots[p1].X
	y0, y1 := f.dots[p0].Y, f.dots[p1].Y

	ratio := Div(x-x0, x1-x0)

	return Mul(y0, piecefunc.DecimalUnit-ratio) + Mul(y1, ratio)
}

func stakeCumDist() (cumDist []float64) {
	// the purpose of this function is to caluclate a cumulative distribution of validator stake for use in creating random samples from the data distribution

	//list of validator stakes in July 2022
	stakeData := [...]float64{198081564.62, 170755849.45, 145995219.17, 136839786.82, 69530006.55, 40463200.25, 39124627.82, 32452971, 29814402.94, 29171276.63, 26284696.12, 25121739.54, 24461049.53, 23823498.37, 22093834.4, 21578984.4, 20799555.11, 19333530.31, 18250949.01, 17773018.94, 17606393.73, 16559031.91, 15950172.21, 12009825.67, 11049478.07, 9419996.86, 9164450.96, 9162745.35, 7822093.53, 7540197.22, 7344958.29, 7215437.9, 6922757.07, 6556643.44, 5510793.7, 5228201.11, 5140257.3, 4076474.17, 3570632.17, 3428553.68, 3256601.94, 3185019, 3119162.23, 3011027.22, 2860160.77, 2164550.78, 1938492.01, 1690762.63, 1629428.73, 1471177.28, 1300562.06, 1237812.75, 1199822.32, 1095856.64, 1042099.38, 1020613.06, 1020055.55, 946528.43, 863022.57, 826015.44, 800010, 730537, 623529.61, 542996.04, 538920.36, 536288, 519803.37, 505401, 502231, 500100, 500001, 500000}
	stakeDataInt := make([]int, len(stakeData))
	// find the maximum stake in the data
	maxStake := 0
	for i, stake := range stakeData {
		stakeDataInt[i] = int(stake)
		if int(stake) > maxStake {
			maxStake = int(stake)
		}
	}
	// calculate the distribution of the data by dividing into bins
	binVals := make([]float64, maxStake+1)
	for _, stake := range stakeDataInt {
		binVals[stake]++
	}

	//now calculate the cumulative distribution of the delay data
	cumDist = make([]float64, len(binVals))
	npts := float64(len(stakeDataInt))
	cumDist[0] = float64(binVals[0]) / npts
	for i := 1; i < len(cumDist); i++ {
		cumDist[i] = cumDist[i-1] + binVals[i]/npts
	}
	return cumDist
}

// FakeLachesis creates empty abft with mem store and equal weights of nodes in genesis.
func SimulatorLachesis(nodes []idx.ValidatorID, weights []pos.Weight, mods ...memorydb.Mod) (*SimLachesis, *Store, *EventStore, *adapters.VectorToDagIndexer) {
	validators := make(pos.ValidatorsBuilder, len(nodes))
	for i, v := range nodes {
		if weights == nil {
			validators[v] = 1
		} else {
			validators[v] = weights[i]
		}
	}

	openEDB := func(epoch idx.Epoch) kvdb.Store {
		return memorydb.New()
	}
	crit := func(err error) {
		panic(err)
	}
	store := NewStore(memorydb.New(), openEDB, crit, LiteStoreConfig())

	err := store.ApplyGenesis(&Genesis{
		Validators: validators.Build(),
		Epoch:      FirstEpoch,
	})
	if err != nil {
		panic(err)
	}

	input := NewEventStore()

	config := LiteConfig()
	dagIndexer := &adapters.VectorToDagIndexer{Index: vecfc.NewIndex(crit, vecfc.LiteConfig())}
	lch := NewIndexedLachesis(store, input, dagIndexer, crit, config)

	extended := &SimLachesis{
		IndexedLachesis: lch,
		blocks:          map[BlockKey]*BlockResult{},
		epochBlocks:     map[idx.Epoch]idx.Frame{},
		confirmationTimer: ConfirmationTimer{allEvents: make([]QITestEvent, 0),
			currentTime: 0},
	}

	err = extended.Bootstrap(lachesis.ConsensusCallbacks{
		BeginBlock: func(block *lachesis.Block) lachesis.BlockCallbacks {
			return lachesis.BlockCallbacks{
				ApplyEvent: func(event dag.Event) {
					extended.ApplyEvent(event)
				},
				EndBlock: func() (sealEpoch *pos.Validators) {
					// track blocks
					key := BlockKey{
						Epoch: extended.store.GetEpoch(),
						Frame: extended.store.GetLastDecidedFrame() + 1,
					}
					extended.blocks[key] = &BlockResult{
						Atropos:    block.Atropos,
						Cheaters:   block.Cheaters,
						Validators: extended.store.GetValidators(),
					}
					// check that prev block exists
					if extended.lastBlock.Epoch != key.Epoch && key.Frame != 1 {
						panic("first frame must be 1")
					}
					extended.epochBlocks[key.Epoch]++
					extended.lastBlock = key
					if extended.applyBlock != nil {
						return extended.applyBlock(block)
					}
					return nil
				},
			}
		},
	})
	if err != nil {
		panic(err)
	}
	return extended, store, input, dagIndexer
}

func updateGas(event *QITestEvent, lch *SimLachesis, validatorGas *ValidatorGas, gasUsed float64) {
	if (*event).SelfParent() != nil {
		// event is a non-leaf event
		// find selfParent QITestEvent
		var selfParentQI QITestEvent
		mutex.Lock()
		for i := len(lch.confirmationTimer.allEvents) - 1; i >= 0; i-- { //search in reverse order because events should normally be recent
			if lch.confirmationTimer.allEvents[i].ID() == *event.SelfParent() {
				selfParentQI = lch.confirmationTimer.allEvents[i]
			}
		}
		mutex.Unlock()

		millisecondsElapsed := 0
		if event.medianTime > selfParentQI.medianTime { // needs to be positive time difference; new event comes after self parent
			millisecondsElapsed = event.medianTime - selfParentQI.medianTime
		}

		// update gas based on time difference
		newAllocatedGasLong := float64(millisecondsElapsed) * validatorGas.ValidatorAllocPerMilliSecLong
		availableGasLong := validatorGas.AvailableLongGas + newAllocatedGasLong
		if availableGasLong > validatorGas.MaxLongGas {
			availableGasLong = validatorGas.MaxLongGas
		}

		newAllocatedGasShort := float64(millisecondsElapsed) * validatorGas.ValidatorAllocPerMilliSecShort
		availableGasShort := validatorGas.AvailableShortGas + newAllocatedGasShort
		if availableGasShort > validatorGas.MaxShortGas {
			availableGasShort = validatorGas.MaxShortGas
		}

		validatorGas.AvailableLongGas = availableGasLong - gasUsed
		validatorGas.AvailableShortGas = availableGasShort - gasUsed
	}
}

func sufficientGas(event *QITestEvent, lch *SimLachesis, validators *pos.Validators, validatorGas *ValidatorGas, gasUsed float64) bool {

	if (*event).SelfParent() != nil { // event is a non-leaf event
		// find selfParent QITestEvent
		var selfParentQI QITestEvent
		mutex.Lock()
		for i := len(lch.confirmationTimer.allEvents) - 1; i >= 0; i-- { //search in reverse order because events should normally be recent
			if lch.confirmationTimer.allEvents[i].ID() == *event.SelfParent() {
				selfParentQI = lch.confirmationTimer.allEvents[i]
			}
		}
		mutex.Unlock()

		millisecondsElapsed := 0
		if event.medianTime > selfParentQI.medianTime {
			millisecondsElapsed = event.medianTime - selfParentQI.medianTime
		}

		// update gas based on time difference
		newAllocatedGasLong := float64(millisecondsElapsed) * validatorGas.ValidatorAllocPerMilliSecLong
		availableGasLong := validatorGas.AvailableLongGas + newAllocatedGasLong
		if availableGasLong > validatorGas.MaxLongGas {
			availableGasLong = validatorGas.MaxLongGas
		}

		newAllocatedGasShort := float64(millisecondsElapsed) * validatorGas.ValidatorAllocPerMilliSecShort
		availableGasShort := validatorGas.AvailableShortGas + newAllocatedGasShort
		if availableGasShort > validatorGas.MaxShortGas {
			availableGasShort = validatorGas.MaxShortGas
		}

		// both long and short gas must be available
		if availableGasLong >= gasUsed && availableGasShort > gasUsed {
			// the validator has sufficient gas to create the event
			return true
		}
		return false // the validator does not have sufficient gas to create the event
	} else {
		//event is a leaf event
		return true
	}

}

func SetEventMedianTime(event *QITestEvent, lch *SimLachesis, validators *pos.Validators) {
	// This function calculates the stake weighted median creation time of event.
	// This corresponds to the stake weighted median creation time of the set of highest before events from all validators
	// Note, for simulation purposes we ignore cheaters etc unlike in go-opera!!!

	// For event, first find the highestBefore of each validator
	median := 0
	// event may be under consideration for creation and therefore not in the DAG, use its parents to find highestBefore
	highestBefore := make(map[idx.ValidatorID]idx.Event)
	for _, parent := range event.Parents() {
		highestBeforeOfParent := lch.dagIndex.GetMergedHighestBefore(parent) // highestBefore sequence numbers of event
		for _, valIdx := range validators.Idxs() {
			if highestBeforeOfParent.Get(valIdx).Seq() > highestBefore[validators.GetID(valIdx)] {
				highestBefore[validators.GetID(valIdx)] = highestBeforeOfParent.Get(valIdx).Seq()
			}
		}
	}

	highestEvents := make([]QITestEvent, validators.Len()) // create a slice of events (to be found)
	// now find the QITestEvent for each of the highestBefore
	mutex.Lock()
	// for highestCreator, highestSeq := range highestBefore {
	for j, valID := range validators.SortedIDs() {
		noEventFound := true
		for i := len(lch.confirmationTimer.allEvents) - 1; i >= 0; i-- { //search in reverse order because events should normally be recent
			if lch.confirmationTimer.allEvents[i].Creator() == valID &&
				lch.confirmationTimer.allEvents[i].Seq() == highestBefore[valID] {
				highestEvents[j] = lch.confirmationTimer.allEvents[i]
				noEventFound = false
				break
			}
		}
		if noEventFound {
			highestEvents[j] = QITestEvent{creationTime: 0} //if no event is observed for this validator, use 0 creation time
		}
	}
	mutex.Unlock()

	// the highest before event for each validator has been obtained, now sort highest before events according to creation time
	sort.Slice(highestEvents, func(i, j int) bool {
		a, b := highestEvents[i], highestEvents[j]
		return a.creationTime < b.creationTime
	})

	// Calculate weighted median from the sorted events as done in go-opera/vcmt/median_time.go MedianTime()
	medianWeight := validators.TotalWeight() / 2
	var currWeight pos.Weight
	for _, highest := range highestEvents {
		currWeight += validators.Get(highest.Creator())
		if currWeight >= medianWeight {
			median = highest.creationTime
			break
		}
	}
	event.medianTime = median
}
