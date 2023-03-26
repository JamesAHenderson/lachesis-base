package quorumIndexer

import (
	"crypto/sha256"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"github.com/Fantom-foundation/lachesis-base/abft"
	"github.com/Fantom-foundation/lachesis-base/emitter/ancestor"
	"github.com/Fantom-foundation/lachesis-base/hash"
	"github.com/Fantom-foundation/lachesis-base/inter/dag"
	"github.com/Fantom-foundation/lachesis-base/inter/dag/tdag"
	"github.com/Fantom-foundation/lachesis-base/inter/idx"
	"github.com/Fantom-foundation/lachesis-base/inter/pos"
)

type Results struct {
	maxFrame  idx.Frame
	numEvents int
}

type NetworkGas struct {
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

type QITestEvents []*QITestEvent

type QITestEvent struct {
	tdag.TestEvent

	// creationTime int
}

var mutex sync.Mutex // a mutex used for variables shared across go rountines

func TestQI(t *testing.T) {
	numNodes := 100
	stakeDist := stakeCumDist()             // for stakes drawn from distribution
	stakeRNG := rand.New(rand.NewSource(0)) // for stakes drawn from distribution

	weights := make([]pos.Weight, numNodes)
	for i, _ := range weights {
		// uncomment one of the below options for valiator stake distribution
		weights[i] = pos.Weight(1)                               //for equal stake
		weights[i] = pos.Weight(sampleDist(stakeRNG, stakeDist)) // for non-equal stake sample from Fantom main net validator stake distribution
	}
	sort.Slice(weights, func(i, j int) bool { return weights[i] > weights[j] }) // sort weights in order
	QIParentCount := 3                                                          // maximum number of parents selected by quorum indexer
	randParentCount := 0                                                        // maximum number of parents selected randomly
	offlineNodes := false                                                       // set to true to make smallest non-quourm validators offline

	// Gas setup
	var NetworkGas NetworkGas
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

	// Latencies between validators are drawn from a Normal Gaussian distribution
	// var latency gaussianLatency
	// latency.mean = 100 // mean latency in milliseconds
	// latency.std = 0    // standard deviation of latency in milliseconds
	// maxLatency := int(latency.mean + 4*latency.std)

	// Latencies between validators are modelled using a dataset of real world internet latencies between cities
	var latency cityLatency
	var seed int64
	seed = 1 //use this for the same seed each time the simulator runs
	// // seed = time.Now().UnixNano() //use this for a different seed each time the simulator runs
	maxLatency := latency.initialise(numNodes, seed)

	// Latencies between validators are drawn from a dataset of latencies observed by one Fantom main net validator. Note all pairs of validators will use the same distribution
	// var latency mainNetLatency
	// maxLatency := latency.initialise()

	// Overlay a P2P network on top of internet
	var P2P P2P
	P2P.useP2P = true
	P2P.numValidators = numNodes
	P2P.maxPeers = 10
	P2P.randSrc = rand.New(rand.NewSource(seed)) // seed RNG for selecting peers
	latency.stdLatencies = make([][]float64, numNodes)
	for i := 0; i < numNodes; i++ {
		latency.stdLatencies[i] = make([]float64, numNodes)
	}
	P2P.internetLatencies = &latency
	// choose a method of peer selection
	// P2P.randomPeers() // each validator has maxPeers chosen randomly with no duplicates
	// P2P.randomSymmetricPeers(false)
	// P2P.closestStakeOrderPeers(weights) // each validator has maxPeers chosen randomly with no duplicates
	// P2P.closestStakePeriodicOrderPeers(weights) // each validator has maxPeers chosen randomly with no duplicates

	// P2P.lowestLatencyAndRandomPeers()
	// P2P.lowestLatencyAndStakeOrderPeers()
	// P2P.lowestLatencyAndStakeOrderPeriodicPeers()
	// P2P.highestLatencyPeers()
	// P2P.smallWorldPeers(weights)
	// P2P.smallWorldPeriodicPeers(weights)
	// P2P.smallWorldPeriodicSymPeers(weights)
	// P2P.LowestStakeDistLatencyPeers(weights)
	// P2P.LowestStakeDistPeriodicLatencyPeers(weights)
	// P2P.bisectionPeers(weights)
	// P2P.bisectionPeers2(weights)

	// P2P.maxPeers = 2
	// P2P.closestStakePeriodicOrderPeers(weights) // each validator has maxPeers chosen randomly with no duplicates
	P2P.maxPeers = 20
	// P2P.exponentialNonPeriodic(weights, false)
	P2P.randomSymmetricPeers(false)

	// P2P.maxPeers = 3
	// P2P.exponentialDistancePeerSelectionLargestFirst(weights, false)

	// P2P.maxPeers = 6
	// P2P.LowestStakeDistPeriodicLatencyPeers(weights, true)
	// P2P.LowestStakeDistPeriodicLatencySymmetricPeers(weights, true)
	// P2P.exponentialDistancePeerSelection(weights, false)

	// P2P.maxPeers = 2
	// P2P.LowestStakeDistPeriodicLatencySymmetricPeers(weights)
	// P2P.maxPeers = 10
	// P2P.randomSymmetricPeers(true)

	P2P.calculateP2PLatencies()
	validatorLatency := P2P.P2PLatencies
	maxLatency = P2P.maxLatency

	fmt.Print("P2Plat=np.array([")
	for i := 0; i < numNodes; i++ {
		fmt.Print("[")
		for j := 0; j < numNodes; j++ {
			fmt.Print(int(validatorLatency.latency(i, j, P2P.randSrc)), ",")
		}
		fmt.Println("],")
	}
	fmt.Println("])")
	fmt.Println("")

	var simulationDuration uint32 = 20000 // length of simulated time in milliseconds

	simulate(weights, NetworkGas, QIParentCount, randParentCount, offlineNodes, &validatorLatency, maxLatency, simulationDuration)

}

func simulate(weights []pos.Weight, networkGas NetworkGas, QIParentCount int, randParentCount int, offlineNodes bool, latency latency, maxLatency int, simulationDuration uint32) Results {

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
	randEvRate := 0.00 // sets the probability that an event will be created randomly

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

	var input *abft.EventStore
	var lch *abft.TestLachesis
	inputs := make([]abft.EventStore, numValidators)
	lchs := make([]abft.TestLachesis, numValidators)
	quorumIndexers := make([]ancestor.QuorumIndexer, numValidators)
	for i := 0; i < numValidators; i++ {
		lch, _, input = abft.FakeLachesis(nodes, weights)
		lchs[i] = *lch
		inputs[i] = *input
		quorumIndexers[i] = *ancestor.NewQuorumIndexer(validators, lch)
	}
	// initialise largest quorum validators as fast
	sortWeights := validators.SortedWeights()
	sortedIDs := validators.SortedIDs()
	for i := 0; i < numValidators; i++ {
		selfStake := weights[i]
		largerStake := validators.NewCounter()
		for j, stake := range sortWeights {
			if stake > selfStake {
				largerStake.Count(sortedIDs[j])
			} else {
				break
			}
		}
		if largerStake.HasQuorum() {
			// quorumIndexers[i].FastOrSlow.Fast = false
			quorumIndexers[i].FastOrSlow.Fast = true
		} else {
			quorumIndexers[i].FastOrSlow.Fast = true
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

	// If requried set smallest non-quorum validators as offline for testing
	// sortWeights := validators.SortedWeights()
	// sortedIDs := validators.SortedIDs()
	onlineStake := validators.TotalWeight()
	online := make(map[idx.ValidatorID]bool)
	for i := len(sortWeights) - 1; i >= 0; i-- {
		online[sortedIDs[i]] = true
		if offlineNodes {
			if onlineStake-sortWeights[i] >= validators.Quorum() {
				onlineStake -= sortWeights[i]
				online[sortedIDs[i]] = false
			}
		}
	}
	var minCheckInterval uint32 = 11 // minimum interval before re-checking if event can be created; currently 11 ms in go-opera
	prevCheckTime := make([]uint32, numValidators)
	minEventCreationInterval := make([]uint32, numValidators) // minimum interval between creating event
	for i, _ := range minEventCreationInterval {
		// minEventCreationInterval[i] = uint32(30 * float64(weights[0]) / float64(weights[i])) // scale minimum emission interval with stake, so that large validators can emit more frequently; roughly approximates available gas
		minEventCreationInterval[i] = minCheckInterval // 11 ms is the current go-opera interval for checking if a new event shoudl be emitted
		// if i == 2 { // chosse a validator that misbehaves
		// 	minEventCreationInterval[i] = 1000 //rand.Intn(10000) // make minimum emission interval large to simulate a misbehaving validator
		// }
	}
	// initial delay to avoid synchronous events
	initialDelay := make([]int, numValidators)
	for i := range initialDelay {
		initialDelay[i] = randSrc.Intn(1000)
	}

	bufferedEvents := make([]QITestEvents, numValidators)

	eventsComplete := make([]int, numValidators)

	// setup flag to indicate leaf event
	isLeaf := make([]bool, numValidators)
	for node := range isLeaf {
		isLeaf[node] = true
	}

	// initialise a variable to count which validator's events are used as parents
	parentCount := make([][]int, numValidators)
	for i := 0; i < numValidators; i++ {
		parentCount[i] = make([]int, numValidators)
	}

	// kEvents := make([][]int, numValidators) // store k for each event for later analysis

	selfParent := make([]QITestEvent, numValidators)

	wg := sync.WaitGroup{} // used for parallel go routines

	timeIdx := maxLatency - 1 // circular buffer time index
	var simTime uint32        // counts simulated time
	simTime = 0

	// now start the simulation
	for simTime < simulationDuration {
		// move forward one timestep
		timeIdx = (timeIdx + 1) % maxLatency
		simTime = simTime + 1
		if simTime%1000 == 0 {
			fmt.Println(" TIME: ", simTime) // print time progress for tracking simulation progression
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
						if lchs[receiveNode].DagIndexer.GetEvent(parent) == nil {
							// a parent is not yet in the DAG, so don't process this event yet
							process[i] = false
							break
						}
					}
					if process[i] {
						// buffered event has all parents in the DAG and can now be processed
						// mutex.Lock()
						processEvent(inputs[receiveNode], &lchs[receiveNode], buffEvent, &quorumIndexers[receiveNode], &headsAll[receiveNode], nodes[receiveNode], simTime)
						// mutex.Unlock()
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

						quorumIndexers[self].SelfParentEvent = selfParent[self].ID() // quorumIndexer needs to know the self's previous event
						if !isLeaf[self] {                                           // only non leaf events have parents
							// iteratively select the best parent from the list of heads using quorum indexer parent selection
							// if malicious { // choose bad parents

							// }
							for j := 0; j < QIParentCount-1; j++ {
								if len(heads) <= 0 {
									//no more heads to choose, adding more parents will not improve DAG progress
									break
								}

								best := quorumIndexers[self].Choose(parents.IDs(), heads.IDs()) //new quorumIndexer

								parents = append(parents, heads[best])
								// remove chosen parent from head options
								heads[best] = heads[len(heads)-1]
								heads = heads[:len(heads)-1]
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
						e.SetCreationTime(simTime)

						// amount of gas used by the candidate event

						createRandEvent := randEvRNG[self].Float64() < randEvRate // used for introducing randomly created events

						if online[selfID] == true {
							// self is online
							passedTime := simTime - selfParent[self].CreationTime()

							if passedTime > minEventCreationInterval[self] {
								e.SetMedianTime(quorumIndexers[self].EventMedianTime(e))
								gasUsed := 0.0 //networkGas.EventCreationGas + float64(len(e.Parents()))*networkGas.ParentGas
								sufficientGas := sufficientGas(e, &lchs[self], &quorumIndexers[self], &ValidatorGas[self], gasUsed)
								if sufficientGas {
									// fmt.Println("sufficient gas")
									eTiming := false
									// kNew := 0
									if !isLeaf[self] {
										eTiming, _ = quorumIndexers[self].DAGProgressEventTimingCondition(e.Parents(), online, passedTime)
									}
									maxPassedTime := passedTime > 5000 // max 5 second interval between events
									if createRandEvent || isLeaf[self] || eTiming || maxPassedTime {

										// mutex.Lock()
										// stakeAbove, numAbove := quorumIndexers[self].RankSelfPerformance()
										// var maxFrame idx.Frame = 0
										// for _, events := range headsAll {
										// 	for _, event := range events {
										// 		if event.Frame() > maxFrame {
										// 			maxFrame = event.Frame()
										// 		}
										// 	}
										// }
										// fmt.Println(self, ": StakeAbove: ", stakeAbove, " NumAbove: ", numAbove, " Fast: ", quorumIndexers[self].FastOrSlow.Fast, " Testing: ", quorumIndexers[self].FastOrSlow.Testing, " MaxFrame:", maxFrame)
										// mutex.Unlock()
										//create an event if (i)a random event is created (ii) is a leaf event, or (iii) event timing condition is met
										for _, parent := range parents {
											//record the creator of the parent for later analysis
											parentCreatorID := parent.Creator()
											for pi := 0; pi < numValidators; pi++ {
												if nodes[pi] == parentCreatorID {
													parentCount[self][pi]++
													break
												}
											}
										}
										isLeaf[self] = false                                    // only create one leaf event
										updateGas(e, &lchs[self], &ValidatorGas[self], gasUsed) // update creator's gas
										//now start propagation of event to other nodes
										delay := 1
										for receiveNode := 0; receiveNode < numValidators; receiveNode++ {
											if receiveNode == self {
												delay = 1 // no delay to send to self (self will 'recieve' its own event after time increment at the top of the main loop)
											} else {
												delay = int(latency.latency(self, receiveNode, latencyRNG[self]))
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

										// fmt.Print(kNew, ",")
										// kEvents[self] = append(kEvents[self], kNew)
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
	var stake pos.Weight
	var fastStake pos.Weight
	numFast := 0

	for self := 0; self < numValidators; self++ {

		fmt.Println("Self: ", self, "Stake: ", weights[self], "Stake Frac: ", float64(weights[self])/float64(validators.TotalWeight()), " Fast: ", quorumIndexers[self].FastOrSlow.Fast, " Testing: ", quorumIndexers[self].FastOrSlow.Update, " Stake Before: ", float64(quorumIndexers[self].FastOrSlow.NumHigherPerf)/float64(validators.TotalWeight()), " ", float64(stake)/float64(validators.TotalWeight()))
		stake += weights[self]
		if quorumIndexers[self].FastOrSlow.Fast && !quorumIndexers[self].FastOrSlow.Update {
			fastStake += weights[self]
			numFast++
		}
	}
	fmt.Println("Fast Stake: ", float64(fastStake)/float64(validators.TotalWeight()), " Num Fast: ", numFast)
	// fmt.Println("Simulated time ", float64(simTime)/1000.0, " seconds")
	// fmt.Println("Number of nodes: ", numValidators)
	numOnlineNodes := 0
	for _, isOnline := range online {
		if isOnline {
			numOnlineNodes++
		}
	}
	// fmt.Println("Number of nodes online: ", numOnlineNodes)
	// fmt.Println("Max Total Parents: ", QIParentCount+randParentCount, " Max QI Parents:", QIParentCount, " Max Random Parents", randParentCount)

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

	fmt.Println("Max Frame: ", maxFrame)
	fmt.Println("[Indicator of TTF] Frames per second: ", (1000.0*float64(maxFrame))/float64(simTime))
	fmt.Println(" Number of Events: ", totalEventsComplete)

	fmt.Println("Event rate per (online) node: ", float64(totalEventsComplete)/float64(numOnlineNodes)/(float64(simTime)/1000.0))
	fmt.Println("[Indictor of gas efficiency] Average events per frame per (online) node: ", (float64(totalEventsComplete))/(float64(maxFrame)))

	fmt.Print("parentCounts=np.array([")
	for i := 0; i < numValidators; i++ {
		fmt.Print("[")
		for j := 0; j < numValidators; j++ {
			fmt.Print(parentCount[i][j], ",")
		}
		fmt.Println("],")
	}
	fmt.Println("])")
	fmt.Println("")

	// fmt.Println("kEvents=np.array([")
	// for validator, kVals := range kEvents {
	// 	fmt.Print("[")
	// 	for ki, k := range kVals {
	// 		if ki < len(kVals)-1 {
	// 			fmt.Print(k, ",")
	// 		} else {
	// 			fmt.Print(k)
	// 		}
	// 	}
	// 	if validator < len(kEvents)-1 {
	// 		fmt.Println("],")
	// 	} else {
	// 		fmt.Println("]")
	// 	}
	// }
	// fmt.Println("])")
	// fmt.Println("")

	var results Results
	results.maxFrame = maxFrame
	results.numEvents = totalEventsComplete
	return results

}

func updateGas(event *QITestEvent, lch *abft.TestLachesis, validatorGas *ValidatorGas, gasUsed float64) {
	if (*event).SelfParent() != nil {
		eventMedianTime := event.MedianTime()
		// event is a non-leaf event
		selfParent := (*event).SelfParent()
		selfParentEvent := lch.DagIndex.GetEvent(*selfParent)
		selfParentMedianTime := selfParentEvent.MedianTime() //quorumIndexer.EventMedianTime(selfParentEvent)

		var millisecondsElapsed uint32
		if eventMedianTime > selfParentMedianTime { // needs to be positive time difference; new event comes after self parent
			millisecondsElapsed = eventMedianTime - selfParentMedianTime
		}

		// update gas based on time difference
		newAllocatedGasLong := millisecondsElapsed * uint32(validatorGas.ValidatorAllocPerMilliSecLong)
		availableGasLong := validatorGas.AvailableLongGas + float64(newAllocatedGasLong)
		if availableGasLong > validatorGas.MaxLongGas {
			availableGasLong = validatorGas.MaxLongGas
		}

		newAllocatedGasShort := millisecondsElapsed * uint32(validatorGas.ValidatorAllocPerMilliSecShort)
		availableGasShort := validatorGas.AvailableShortGas + float64(newAllocatedGasShort)
		if availableGasShort > validatorGas.MaxShortGas {
			availableGasShort = validatorGas.MaxShortGas
		}

		validatorGas.AvailableLongGas = availableGasLong - gasUsed
		validatorGas.AvailableShortGas = availableGasShort - gasUsed
		// validators := lch.Store.GetValidators()
		// fracWeight := float64(validators.Get(event.Creator())) / float64(validators.TotalWeight())
		// fmt.Println(" Validator % stake: ", fracWeight, " Used: ", gasUsed, " Remaining long gas: ", validatorGas.AvailableLongGas, " Remaining short gas: ", validatorGas.AvailableShortGas)

	}
}

func sufficientGas(event *QITestEvent, lch *abft.TestLachesis, quorumIndexer *ancestor.QuorumIndexer, validatorGas *ValidatorGas, gasUsed float64) bool {

	if (*event).SelfParent() != nil {
		eventMedianTime := quorumIndexer.EventMedianTime(event)
		event.SetMedianTime(eventMedianTime)
		// event is a non-leaf event
		selfParent := (*event).SelfParent()
		selfParentEvent := lch.DagIndex.GetEvent(*selfParent)
		selfParentMedianTime := selfParentEvent.MedianTime() //quorumIndexer.EventMedianTime(selfParentEvent)

		var millisecondsElapsed uint32
		if eventMedianTime > selfParentMedianTime {
			millisecondsElapsed = eventMedianTime - selfParentMedianTime
		}

		// update gas based on time difference
		newAllocatedGasLong := millisecondsElapsed * uint32(validatorGas.ValidatorAllocPerMilliSecLong)
		availableGasLong := validatorGas.AvailableLongGas + float64(newAllocatedGasLong)
		if availableGasLong > validatorGas.MaxLongGas {
			availableGasLong = validatorGas.MaxLongGas
		}

		newAllocatedGasShort := millisecondsElapsed * uint32(validatorGas.ValidatorAllocPerMilliSecShort)
		availableGasShort := validatorGas.AvailableShortGas + float64(newAllocatedGasShort)
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

func processEvent(input abft.EventStore, lchs *abft.TestLachesis, e *QITestEvent, quorumIndexer *ancestor.QuorumIndexer, heads *dag.Events, self idx.ValidatorID, time uint32) (frame idx.Frame) {
	input.SetEvent(e)

	lchs.DagIndexer.Add(e)
	lchs.Lachesis.Build(e)
	lchs.Lachesis.Process(e)

	lchs.DagIndexer.Flush()
	// quorum indexer needs to process the event
	quorumIndexer.ProcessEvent(&e.BaseEvent, self == e.Creator(), e.CreationTime())

	updateHeads(e, heads)
	return lchs.DagIndexer.GetEvent(e.ID()).Frame()
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
