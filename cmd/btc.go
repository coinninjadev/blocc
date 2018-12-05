package cmd

import (
	cli "github.com/spf13/cobra"
	"go.uber.org/zap"

	"git.coinninja.net/backend/blocc/blocc/btc"
	"git.coinninja.net/backend/blocc/blockstore/esearch"
	"git.coinninja.net/backend/blocc/cache/redis"
	"git.coinninja.net/backend/blocc/conf"
)

func init() {
	rootCmd.AddCommand(btcCmd)
}

var (
	btcCmd = &cli.Command{
		Use:   "btc",
		Short: "BTC Extractor",
		Long:  `BTC Extractor`,
		Run: func(cmd *cli.Command, args []string) { // Initialize the databse

			// Setup the BlockStore
			var err error
			var bs btc.BlockStore
			bs, err = esearch.New()
			if err != nil {
				logger.Fatalw("BlockStore Error", "error", err)
			}

			var mp btc.TxMemPool
			mp, err = redis.New(btc.Symbol + ":mempool:")
			if err != nil {
				logger.Fatalw("BlockCache Error", "error", err)
			}

			// Start the extractor
			_, err = btc.Extract(bs, mp)
			if err != nil {
				logger.Fatalw("Could not create Extractor",
					"error", err,
				)
			}

			<-conf.Stop.Chan() // Wait until StopChan
			conf.Stop.Wait()   // Wait until everyone cleans up
			zap.L().Sync()     // Flush the logger

		},
	}
)
