package btc

import (
	"fmt"
	"net"
	"reflect"
	"sync"
	"time"

	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/peer"
	"github.com/btcsuite/btcd/wire"
	config "github.com/spf13/viper"
	"go.uber.org/zap"

	"git.coinninja.net/backend/blocc/blocc"
	"git.coinninja.net/backend/blocc/conf"
	"git.coinninja.net/backend/blocc/store"
)

const (
	Symbol            = "btc"
	ScriptTypeUnknown = "unknown"
)

type Extractor struct {
	logger      *zap.SugaredLogger
	peer        *peer.Peer
	chainParams *chaincfg.Params
	bcs         blocc.BlockChainStore
	txp         blocc.TxPool
	txb         blocc.TxBus
	ms          blocc.MetricStore
	btm         blocc.BlockTxMonitor
	dc          store.DistCache

	storeRawBlocks       bool
	storeRawTransactions bool

	// This is used to determine how much complete data we have in our database
	validBlockId        string
	validBlockHeight    int64
	lastBlockTime       time.Time
	expectedBlockHeight int64

	blockMonitorTimeout  time.Duration
	blockMonitorLifetime time.Duration

	txLifetime time.Duration

	sync.WaitGroup
	sync.Mutex
}

func Extract(bcs blocc.BlockChainStore, txp blocc.TxPool, txb blocc.TxBus, ms blocc.MetricStore, dc store.DistCache) (*Extractor, error) {

	e := &Extractor{
		logger: zap.S().With("package", "blocc.btc"),
		bcs:    bcs,
		txp:    txp,
		txb:    txb,
		ms:     ms,
		btm:    blocc.NewBlockTxMonitorMem(),
		dc:     dc,

		storeRawBlocks:       config.GetBool("extractor.btc.store_raw_blocks"),
		storeRawTransactions: config.GetBool("extractor.btc.store_raw_transactions"),

		blockMonitorTimeout:  config.GetDuration("extractor.btc.block_monitor_timeout"),
		blockMonitorLifetime: config.GetDuration("extractor.btc.block_monitor_lifetime"),

		txLifetime: config.GetDuration("extractor.btc.transaction_lifetime"),
	}

	var err error

	// Initialize the BlockChainStore for BTC
	if bcs != nil {
		err = e.bcs.Init(Symbol)
		if err != nil {
			return nil, fmt.Errorf("Could not Init BlockChainStore: %s", err)
		}
	}

	// Initialize the TxPool for BTC
	if txp != nil {
		err = e.txp.Init(Symbol)
		if err != nil {
			return nil, fmt.Errorf("Could not Init TxPool: %s", err)
		}
	}

	// Initialize the TxBus for BTC
	if txb != nil {
		err = e.txb.Init(Symbol)
		if err != nil {
			return nil, fmt.Errorf("Could not Init TxBus: %s", err)
		}
	}

	// Initialize the MetricStire for BTC
	if ms != nil {
		err = e.ms.Init(Symbol)
		if err != nil {
			return nil, fmt.Errorf("Could not Init MetricStore: %s", err)
		}
	}

	// Create an array of chains such that we can pick the one we want
	chains := []*chaincfg.Params{
		&chaincfg.MainNetParams,
		&chaincfg.RegressionNetParams,
		&chaincfg.SimNetParams,
		&chaincfg.TestNet3Params,
	}
	// Find the selected chain
	for _, cp := range chains {
		if config.GetString("extractor.btc.chain") == cp.Name {
			e.chainParams = cp
			break
		}
	}
	if e.chainParams == nil {
		return nil, fmt.Errorf("Could not find chain %s", config.GetString("extractor.btc.chain"))
	}

	// When we get a verack message we are ready to process
	ready := make(chan struct{})

	peerConfig := &peer.Config{
		UserAgentName:    conf.Executable, // User agent name to advertise.
		UserAgentVersion: conf.GitVersion, // User agent version to advertise.
		ChainParams:      e.chainParams,
		Services:         wire.SFNodeWitness,
		TrickleInterval:  time.Second * 10,
		Listeners: peer.MessageListeners{
			OnBlock: e.OnBlock,
			OnTx:    e.OnTx,
			OnInv:   e.OnInv,
			OnVerAck: func(p *peer.Peer, msg *wire.MsgVerAck) {
				e.logger.Debug("Got VerAck")
				close(ready)
			},
		},
	}

	// Do we want to see debug messages
	if config.GetBool("extractor.btc.debug_messages") {
		peerConfig.Listeners.OnRead = e.OnRead
		peerConfig.Listeners.OnWrite = e.OnWrite
	}

	// Create peer connection
	e.peer, err = peer.NewOutboundPeer(peerConfig, net.JoinHostPort(config.GetString("extractor.btc.host"), config.GetString("extractor.btc.port")))
	if err != nil {
		return nil, fmt.Errorf("Could not create outbound peer: %v", err)
	}

	// Establish the connection to the peer address and mark it connected.
	conn, err := net.Dial("tcp", e.peer.Addr())
	if err != nil {
		return nil, fmt.Errorf("Could not Dial peer: %v", err)
	}

	// Start it up
	e.peer.AssociateConnection(conn)

	// Wait until ready or timeout
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("Never got verack ready message")
	}

	e.logger.Infow("Connected to peer", "peer", e.peer.Addr(), "height", e.peer.StartingHeight(), "last_block", e.peer.LastBlock())

	// Did we provide a blockchain store? If so, go fetch the block chain
	if e.bcs != nil {
		go e.fetchBlockChain()
	}

	// Get the mempool from the peer
	if e.txp != nil {
		e.RequestMemPool()
	}

	// Close the peer if stop signal comes in and clean everything up
	go func() {
		conf.Stop.Add(1) // Hold shutdown until everything flushed
		<-conf.Stop.Chan()
		e.peer.Disconnect()
		e.btm.Shutdown() // Shutdown the monitor
		e.Wait()         // Wait until all blocks are handled
		if e.bcs != nil {
			e.logger.Info("Flushing BlockChainStore")
			e.bcs.FlushBlocks(Symbol)
			e.bcs.FlushTransactions(Symbol)
		}
		conf.Stop.Done()
	}()

	return e, nil

}

// fetchBlockChain will start fetching blocks until it has the entire block chain
func (e *Extractor) fetchBlockChain() {

	// Figure out the top block in the store
	validBlockId, validBlockHeight, err := e.bcs.GetBlockHeight(Symbol)
	if err != nil && err != store.ErrNotFound {
		e.logger.Fatalw("GetBlockHeight", "error", err)
	} else {
		if err == store.ErrNotFound || e.getValidBlockHeight() < config.GetInt64("extractor.btc.start_block_height") {
			// Set to the start block if we don't have any or for some reason we were requested to start higher
			validBlockId = config.GetString("extractor.btc.start_block_id")
			validBlockHeight = config.GetInt64("extractor.btc.start_block_height")
		}
	}
	e.setValidBlock(validBlockId, validBlockHeight)

	// If we're starting at the genesis block, insert it
	if validBlockHeight == -1 {
		e.handleBlock(e.chainParams.GenesisBlock)
		// The genesis block is now the valid block
		e.setValidBlock(e.chainParams.GenesisBlock.BlockHash().String(), 0)
	}

	e.logger.Infow("Starting block extraction", "start_block_id", e.getValidBlockId(), "start_block_height", e.getValidBlockHeight())

	for !conf.Stop.Bool() {
		start := time.Now()

		validBlockId, validBlockHeight, _ := e.getValidBlock()

		// If the last block we've received is the valid block height, we're caught up
		if int64(e.peer.LastBlock()) == validBlockHeight {
			time.Sleep(time.Second)
			continue
		}

		// Expire other blocks below this block from the blockTxMonitor, we no longer need them
		e.btm.ExpireBelowBlockHeight(validBlockHeight)

		// This will fetch blocks, the first block will be the one after this one and will return extractor.btc.blocks_request_count (500) blocks
		expectedBlockHeight := validBlockHeight + config.GetInt64("extractor.btc.blocks_request_count")
		e.logger.Debugw("Requesting blocks from", "block_id", validBlockId, "block_height", validBlockHeight, "expected", expectedBlockHeight)

		// Fetch blocks
		e.RequestBlocks(validBlockId, "0")
		// Testing, stop at block 10k
		// e.RequestBlocks(e.getValidBlockId(), "0000000099c744455f58e6c6e98b671e1bf7f37346bfd4cf5d0274ad8ee660cb")

		// If we have no block for extractor.btc.block_timeout, assume it stalled
		blockTimeout := make(chan struct{})
		go func() {
			for {
				select {
				case <-blockTimeout:
					return
				case <-time.After(time.Minute):
					// Still working
				}
				if time.Now().Sub(e.getLastBlockTime()) > config.GetDuration("extractor.btc.block_timeout") {
					close(blockTimeout)
					return
				}
			}
		}()

		select {
		// Otherwise, wait for the the last block in the stream of blocks
		case blk := <-e.btm.WaitForBlockHeight(expectedBlockHeight, config.GetDuration("extractor.btc.blocks_request_timeout")):
			close(blockTimeout) // Cancel the block timeout monitor
			if blk == nil {
				e.logger.Errorw("Did not get block when following blockchain", "expected", expectedBlockHeight)
				e.logger.Warnw("Continuing block extraction after timeout", "block_id", e.getValidBlockId(), "block_height", e.getValidBlockHeight())
				continue
			} else {
				e.logger.Infow("Block Chain Stats",
					"height", blk.Height,
					"rate(/h)", 500.0/(time.Now().Sub(start).Hours()),
					"rate(/m)", 500.0/time.Now().Sub(start).Minutes(),
					"rate(/s)", 500.0/time.Now().Sub(start).Seconds(),
					"eta", (time.Duration(float64(int64(e.peer.LastBlock())-expectedBlockHeight)/(500.0/time.Now().Sub(start).Seconds())) * time.Second).String(),
				)
			}
		// No block for extractor.btc.block_timeout
		case <-blockTimeout:
			e.logger.Errorw("Block timeout", "block_id", e.getValidBlockId(), "block_height", e.getValidBlockHeight(), "expected_height", expectedBlockHeight)

			// We're exiting
		case <-conf.Stop.Chan():
		}

	}

}

// RequestBlocks will send a GetBlocks Message to the peer
func (e *Extractor) RequestBlocks(start string, stop string) error {

	startHash, err := chainhash.NewHashFromStr(start)
	if err != nil {
		return fmt.Errorf("NewHashFromStr: error %v\n", err)
	}
	var locator blockchain.BlockLocator = []*chainhash.Hash{startHash}

	// Stophash - All zero means fetch 500
	stopHash, err := chainhash.NewHashFromStr(stop)
	if err != nil {
		return fmt.Errorf("NewHashFromStr: error %v\n", err)
	}

	err = e.peer.PushGetBlocksMsg(locator, stopHash)
	if err != nil {
		return fmt.Errorf("PushGetBlocksMsg: error %v\n", err)
	}

	return nil

}

// RequestMemPool will send a request for the peers mempool
func (e *Extractor) RequestMemPool() {
	e.peer.QueueMessage(wire.NewMsgMemPool(), nil)
}

// OnTx is called when we receive a transaction
func (e *Extractor) OnTx(p *peer.Peer, msg *wire.MsgTx) {
	go e.handleTx(nil, nil, blocc.HeightUnknown, msg)
}

// OnBlock is called when we receive a block message
func (e *Extractor) OnBlock(p *peer.Peer, msg *wire.MsgBlock, buf []byte) {
	go e.handleBlock(msg)
}

// OnInv is called when the peer reports it has an inventory item
func (e *Extractor) OnInv(p *peer.Peer, msg *wire.MsgInv) {

	// OnInv is invoked when a peer receives an inv bitcoin message. This is essentially the peer saying I have this piece of information
	// We immediately request that piece of information if it's a transaction or a block
	for _, iv := range msg.InvList {
		switch iv.Type {
		case wire.InvTypeTx:
			e.logger.Debugw("Got Inv", "type", iv.Type, "txid", iv.Hash.String())
			msg := wire.NewMsgGetData()
			err := msg.AddInvVect(iv)
			if err != nil {
				e.logger.Errorw("AddInvVect", "error", err)
			}
			p.QueueMessage(msg, nil)
		case wire.InvTypeBlock:
			e.logger.Debugw("Got Inv", "type", iv.Type, "txid", iv.Hash.String())
			msg := wire.NewMsgGetData()
			err := msg.AddInvVect(iv)
			if err != nil {
				e.logger.Errorw("AddInvVect", "error", err)
			}
			p.QueueMessage(msg, nil)
		}
	}
}

// OnRead is a low level function to capture raw messages coming in
func (e *Extractor) OnRead(p *peer.Peer, bytesRead int, msg wire.Message, err error) {
	e.logger.Debugw("Got Message", "type", reflect.TypeOf(msg), "size", bytesRead, "error", err)
}

// OnWrite is a low level function to capture raw message going out
func (e *Extractor) OnWrite(p *peer.Peer, bytesWritten int, msg wire.Message, err error) {
	e.logger.Debugw("Sent Message", "type", reflect.TypeOf(msg), "size", bytesWritten, "error", err)
}

func (e *Extractor) getValidBlockHeight() int64 {
	e.Lock()
	defer e.Unlock()
	return e.validBlockHeight
}

func (e *Extractor) getValidBlockId() string {
	e.Lock()
	defer e.Unlock()
	return e.validBlockId
}

func (e *Extractor) getLastBlockTime() time.Time {
	e.Lock()
	defer e.Unlock()
	return e.lastBlockTime
}

func (e *Extractor) getValidBlock() (string, int64, time.Time) {
	e.Lock()
	defer e.Unlock()
	return e.validBlockId, e.validBlockHeight, e.lastBlockTime
}

func (e *Extractor) setValidBlock(blockId string, blockHeight int64) {
	e.Lock()
	defer e.Unlock()
	e.validBlockId = blockId
	e.validBlockHeight = blockHeight
	e.lastBlockTime = time.Now()
}
