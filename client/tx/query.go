package tx

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/context"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/rest"
	"github.com/gorilla/mux"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tendermint/tendermint/types"

	authTypes "github.com/maticnetwork/heimdall/auth/types"
	"github.com/maticnetwork/heimdall/helper"
	hmRest "github.com/maticnetwork/heimdall/types/rest"
)

const (
	flagTags  = "tags"
	flagPage  = "page"
	flagLimit = "limit"
)

// ----------------------------------------------------------------------------
// CLI
// ----------------------------------------------------------------------------

// SearchTxCmd returns a command to search through tagged transactions.
func SearchTxCmd(cdc *codec.Codec) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "txs",
		Short: "Search for paginated transactions that match a set of tags",
		Long: strings.TrimSpace(`
Search for transactions that match the exact given tags where results are paginated.

Example:
$ gaiacli query txs --tags '<tag1>:<value1>&<tag2>:<value2>' --page 1 --limit 30
`),
		RunE: func(cmd *cobra.Command, args []string) error {
			tagsStr := viper.GetString(flagTags)
			tagsStr = strings.Trim(tagsStr, "'")

			var tags []string
			if strings.Contains(tagsStr, "&") {
				tags = strings.Split(tagsStr, "&")
			} else {
				tags = append(tags, tagsStr)
			}

			var tmTags []string
			for _, tag := range tags {
				if !strings.Contains(tag, ":") {
					return fmt.Errorf("%s should be of the format <key>:<value>", tagsStr)
				} else if strings.Count(tag, ":") > 1 {
					return fmt.Errorf("%s should only contain one <key>:<value> pair", tagsStr)
				}

				keyValue := strings.Split(tag, ":")
				if keyValue[0] == types.TxHeightKey {
					tag = fmt.Sprintf("%s=%s", keyValue[0], keyValue[1])
				} else {
					tag = fmt.Sprintf("%s='%s'", keyValue[0], keyValue[1])
				}
				tmTags = append(tmTags, tag)
			}

			page := viper.GetInt(flagPage)
			limit := viper.GetInt(flagLimit)

			cliCtx := context.NewCLIContext().WithCodec(cdc)
			txs, err := helper.SearchTxs(cliCtx, cdc, tmTags, page, limit)
			if err != nil {
				return err
			}

			var output []byte
			if cliCtx.Indent {
				output, err = cdc.MarshalJSONIndent(txs, "", "  ")
			} else {
				output, err = cdc.MarshalJSON(txs)
			}

			if err != nil {
				return err
			}

			fmt.Println(string(output))
			return nil
		},
	}

	cmd.Flags().StringP(client.FlagNode, "n", "tcp://localhost:26657", "Node to connect to")
	viper.BindPFlag(client.FlagNode, cmd.Flags().Lookup(client.FlagNode))
	cmd.Flags().Bool(client.FlagTrustNode, false, "Trust connected full node (don't verify proofs for responses)")
	viper.BindPFlag(client.FlagTrustNode, cmd.Flags().Lookup(client.FlagTrustNode))

	cmd.Flags().String(flagTags, "", "tag:value list of tags that must match")
	cmd.Flags().Uint32(flagPage, rest.DefaultPage, "Query a specific page of paginated results")
	cmd.Flags().Uint32(flagLimit, rest.DefaultLimit, "Query number of transactions results per page returned")
	cmd.MarkFlagRequired(flagTags)

	return cmd
}

// QueryTxCmd implements the default command for a tx query.
func QueryTxCmd(cdc *codec.Codec) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tx [hash]",
		Short: "Find a transaction by hash in a committed block.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cliCtx := context.NewCLIContext().WithCodec(cdc)

			output, err := helper.QueryTx(cdc, cliCtx, args[0])
			if err != nil {
				return err
			}

			if output.Empty() {
				return fmt.Errorf("No transaction found with hash %s", args[0])
			}

			return cliCtx.PrintOutput(output)
		},
	}

	cmd.Flags().StringP(client.FlagNode, "n", "tcp://localhost:26657", "Node to connect to")
	viper.BindPFlag(client.FlagNode, cmd.Flags().Lookup(client.FlagNode))
	cmd.Flags().Bool(client.FlagTrustNode, false, "Trust connected full node (don't verify proofs for responses)")
	viper.BindPFlag(client.FlagTrustNode, cmd.Flags().Lookup(client.FlagTrustNode))

	return cmd
}

// ----------------------------------------------------------------------------
// REST
// ----------------------------------------------------------------------------

// QueryTxsByTagsRequestHandlerFn implements a REST handler that searches for
// transactions by tags.
func QueryTxsByTagsRequestHandlerFn(cliCtx context.CLIContext, cdc *codec.Codec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var (
			tags        []string
			txs         []sdk.TxResponse
			page, limit int
		)

		err := r.ParseForm()
		if err != nil {
			rest.WriteErrorResponse(w, http.StatusBadRequest,
				sdk.AppendMsgToErr("could not parse query parameters", err.Error()))
			return
		}

		if len(r.Form) == 0 {
			rest.PostProcessResponse(w, cdc, txs, cliCtx.Indent)
			return
		}

		tags, page, limit, err = rest.ParseHTTPArgs(r)
		if err != nil {
			rest.WriteErrorResponse(w, http.StatusBadRequest, err.Error())
			return
		}

		txs, err = helper.SearchTxs(cliCtx, cdc, tags, page, limit)
		if err != nil {
			rest.WriteErrorResponse(w, http.StatusInternalServerError, err.Error())
			return
		}

		rest.PostProcessResponse(w, cdc, txs, cliCtx.Indent)
	}
}

// QueryTxRequestHandlerFn implements a REST handler that queries a transaction
// by hash in a committed block.
func QueryTxRequestHandlerFn(cliCtx context.CLIContext, cdc *codec.Codec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		hashHexStr := vars["hash"]

		output, err := helper.QueryTx(cdc, cliCtx, hashHexStr)
		if err != nil {
			if strings.Contains(err.Error(), hashHexStr) {
				rest.WriteErrorResponse(w, http.StatusNotFound, err.Error())
				return
			}
			rest.WriteErrorResponse(w, http.StatusInternalServerError, err.Error())
			return
		}

		if output.Empty() {
			rest.WriteErrorResponse(w, http.StatusNotFound, fmt.Sprintf("no transaction found with hash %s", hashHexStr))
		}

		rest.PostProcessResponse(w, cdc, output, cliCtx.Indent)
	}
}

// QueryCommitTxRequestHandlerFn implements a REST handler that queries vote, sigs and tx bytes committed block.
func QueryCommitTxRequestHandlerFn(cliCtx context.CLIContext, cdc *codec.Codec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)

		hash, err := hex.DecodeString(vars["hash"])
		if err != nil {
			rest.WriteErrorResponse(w, http.StatusInternalServerError, err.Error())
			return
		}

		tx, err := helper.QueryTxWithProof(cliCtx, hash)
		if err != nil {
			if strings.Contains(err.Error(), vars["hash"]) {
				rest.WriteErrorResponse(w, http.StatusNotFound, err.Error())
				return
			}
			rest.WriteErrorResponse(w, http.StatusInternalServerError, err.Error())
			return
		}

		// get block client
		blockDetails, err := helper.GetBlock(cliCtx, tx.Height+1)

		// extract signs from votes
		sigs := helper.GetSigs(blockDetails.Block.LastCommit.Precommits)

		// proof
		proofList := helper.GetMerkleProofList(&tx.Proof.Proof)
		proof := helper.AppendBytes(proofList...)

		// commit tx proof
		result := hmRest.CommitTxProof{
			Vote:  hex.EncodeToString(helper.GetVoteBytes(blockDetails.Block.LastCommit.Precommits, blockDetails.Block.ChainID)),
			Sigs:  hex.EncodeToString(sigs),
			Tx:    hex.EncodeToString(tx.Tx[authTypes.PulpHashLength:]),
			Proof: hex.EncodeToString(proof),
		}

		rest.PostProcessResponse(w, cdc, result, cliCtx.Indent)
	}
}