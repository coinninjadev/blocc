package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/render"
	"github.com/spf13/cast"

	"git.coinninja.net/backend/blocc/blocc"
	"git.coinninja.net/backend/blocc/blocc/btc"
	"git.coinninja.net/backend/blocc/store"
)

// LegacyFindAddressTransactions returns Transactions based on addresses
func (s *Server) LegacyFindAddressTransactions(method string) http.HandlerFunc {

	type AddressTransaction struct {
		Address      string `json:"address"`
		BlockHash    string `json:"blockhash"`
		ReceivedTime int64  `json:"received_time"`
		TxID         string `json:"txid"`
		Time         int64  `json:"time"`
		VIn          int64  `json:"vin"`
		VOut         int64  `json:"vout"`
	}

	paginateSlice := func(x []*AddressTransaction, skip int, size int) []*AddressTransaction {
		if skip > len(x) {
			skip = len(x)
		}

		end := skip + size
		if end > len(x) {
			end = len(x)
		}

		return x[skip:end]
	}

	return func(w http.ResponseWriter, r *http.Request) {

		var page int
		var perPage int
		var slicePerPage int
		var slicePage int
		var start *time.Time
		var end *time.Time
		var addresses []string

		switch method {
		case http.MethodGet:
			// Handle paging
			page = cast.ToInt(r.URL.Query().Get("page"))
			if page <= 0 {
				page = 1
			}
			perPage = cast.ToInt(r.URL.Query().Get("perPage"))
			if perPage <= 0 {
				perPage = s.defaultCount
			}
			// Handle time
			start = blocc.ParseUnixTime(cast.ToInt64(r.URL.Query().Get("start")))
			end = blocc.ParseUnixTime(cast.ToInt64(r.URL.Query().Get("end")))

			// Get the address
			addresses = []string{chi.URLParam(r, "address")}
		case http.MethodPost:

			var postData struct {
				Query struct {
					Terms struct {
						Address   []string `json:"address"`
						TimeAfter int64    `json:"time_after"`
					} `json:"terms"`
				} `json:"query"`
			}
			if err := render.DecodeJSON(r.Body, &postData); err != nil {
				render.Render(w, r, ErrInvalidRequest(err))
				return
			}
			if len(postData.Query.Terms.Address) == 0 {
				render.Render(w, r, ErrInvalidRequest(fmt.Errorf("You need to provide at least one address")))
			}

			page = 1
			perPage = store.CountMax
			if postData.Query.Terms.TimeAfter < 0 {
				start = blocc.ParseUnixTime(time.Now().Unix() + postData.Query.Terms.TimeAfter)
			} else if postData.Query.Terms.TimeAfter != 0 {
				start = blocc.ParseUnixTime(postData.Query.Terms.TimeAfter + 1)
			}
			addresses = postData.Query.Terms.Address
		}

		// Get transactions
		txs, err := s.blockChainStore.FindTxsByAddressesAndTime(btc.Symbol, addresses, start, end, blocc.TxFilterAddressInputOutput, blocc.TxIncludeHeader|blocc.TxIncludeIn|blocc.TxIncludeOut, (page-1)*perPage, perPage)
		if err != nil && err != blocc.ErrNotFound {
			render.Render(w, r, ErrInvalidRequest(err))
			return
		}

		ret := make([]*AddressTransaction, 0)
		sliceRet := make([]*AddressTransaction, 0)

		for _, tx := range txs {
			for _, address := range addresses {
				found := false

				at := &AddressTransaction{
					Address:      address,
					BlockHash:    tx.BlockId,
					TxID:         tx.TxId,
					ReceivedTime: tx.Time,
					Time:         tx.BlockTime,
				}

				// mobile apps are expecting this to be an empty string if it's unconfirmed
				if at.BlockHash == blocc.BlockIdMempool {
					at.BlockHash = ""
				}

				// mobile apps are expecting this to be the same as recevied_time if it's unconfirmed
				if at.Time == 0 {
					at.Time = at.ReceivedTime
				}

				// Find Vin
				for _, txIn := range tx.In {
					if txIn.Out != nil && txIn.Out.Addresses != nil {
						for _, a := range txIn.Out.Addresses {
							if a == address {
								found = true
								at.VIn += txIn.Out.Value
							}
						}
					}
				}
				// Find Vout
				for _, txOut := range tx.Out {
					if txOut.Addresses != nil {
						for _, a := range txOut.Addresses {
							if a == address {
								found = true
								at.VOut += txOut.Value
							}
						}
					}
				}
				if found {
					ret = append(ret, at)
				}
			}
		}

		//Simple offset / count strategy for delivering a portion of the addressTx result set.
		slicePage = cast.ToInt(r.URL.Query().Get("page"))
		if slicePage <= 0 {
			slicePage = 1
			slicePerPage = store.CountMax
		} else {
			slicePerPage = cast.ToInt(r.URL.Query().Get("perPage"))
			if slicePerPage <= 0 {
				slicePerPage = s.defaultCount
			}
		}
		sliceRet = paginateSlice(ret, (slicePage-1)*slicePerPage, slicePerPage)

		render.JSON(w, r, sliceRet)
	}

}

// LegacyGetAddressStats gets address statistics
func (s *Server) LegacyGetAddressStats() http.HandlerFunc {

	type AddressStats struct {
		Address  string `json:"address"`
		Balance  int64  `json:"balance"`
		Received int64  `json:"received"`
		Spent    int64  `json:"spent"`
		TxCount  int64  `json:"tx_count"`
	}

	return func(w http.ResponseWriter, r *http.Request) {

		var ret AddressStats
		var err error

		// Get the address
		ret.Address = chi.URLParam(r, "address")

		// Get the stats
		ret.TxCount, ret.Received, ret.Spent, err = s.blockChainStore.GetAddressStats(btc.Symbol, ret.Address)
		if err != nil && err != blocc.ErrNotFound {
			render.Render(w, r, ErrInvalidRequest(err))
			return
		}

		ret.Balance = ret.Received - ret.Spent

		render.JSON(w, r, ret)
	}

}
