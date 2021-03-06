package commands

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"

	humanize "gx/ipfs/QmPSBJL4momYnE7DcUyk2DVhD6rH488ZmHBGLbxNdhU44K/go-humanize"

	cmds "github.com/ipfs/go-ipfs/commands"
	metrics "gx/ipfs/QmWpTXhTkpoCDEm9twJd5Rc9jFwy61emzxneeJzrVMfjGF/go-libp2p-metrics"
	protocol "gx/ipfs/QmZNkThpqfVXs9GNbexPrfBbXSLNYeKrE7jwFM2oqHbyqN/go-libp2p-protocol"
	u "gx/ipfs/Qmb912gdngC1UWwTkhuW8knyRbcWeu5kqkxBpveLmW8bSr/go-ipfs-util"
	peer "gx/ipfs/QmfMmLGoKzCHDN7cGgk64PJr4iipzidDRME8HABSJqvmhC/go-libp2p-peer"
)

var StatsCmd = &cmds.Command{
	Helptext: cmds.HelpText{
		Tagline: "Query ipfs statistics.",
		ShortDescription: `'ipfs stats' is a set of commands to help look at statistics
for your ipfs node.
`,
		LongDescription: `'ipfs stats' is a set of commands to help look at statistics
for your ipfs node.`,
	},

	Subcommands: map[string]*cmds.Command{
		"bw":      statBwCmd,
		"repo":    repoStatCmd,
		"bitswap": bitswapStatCmd,
	},
}

var statBwCmd = &cmds.Command{
	Helptext: cmds.HelpText{
		Tagline: "Print ipfs bandwidth information.",
		ShortDescription: `'ipfs stats bw' prints bandwidth information for the ipfs daemon.
It displays: TotalIn, TotalOut, RateIn, RateOut.
		`,
		LongDescription: `'ipfs stats bw' prints bandwidth information for the ipfs daemon.
It displays: TotalIn, TotalOut, RateIn, RateOut.

By default, overall bandwidth and all protocols are shown. To limit bandwidth
to a particular peer, use the 'peer' option along with that peer's multihash
id. To specify a specific protocol, use the 'proto' option. The 'peer' and
'proto' options cannot be specified simultaneously. The protocols that are
queried using this method are outlined in the specification:
https://github.com/ipfs/specs/blob/master/libp2p/7-properties.md#757-protocol-multicodecs

Example protocol options:
  - /ipfs/id/1.0.0
  - /ipfs/bitswap
  - /ipfs/dht

Example:

    > ipfs stats bw -t /ipfs/bitswap
    Bandwidth
    TotalIn: 5.0MB
    TotalOut: 0B
    RateIn: 343B/s
    RateOut: 0B/s
    > ipfs stats bw -p QmepgFW7BHEtU4pZJdxaNiv75mKLLRQnPi1KaaXmQN4V1a
    Bandwidth
    TotalIn: 4.9MB
    TotalOut: 12MB
    RateIn: 0B/s
    RateOut: 0B/s
`,
	},
	Options: []cmds.Option{
		cmds.StringOption("peer", "p", "Specify a peer to print bandwidth for."),
		cmds.StringOption("proto", "t", "Specify a protocol to print bandwidth for."),
		cmds.BoolOption("poll", "Print bandwidth at an interval.").Default(false),
		cmds.StringOption("interval", "i", `Time interval to wait between updating output, if 'poll' is true.

    This accepts durations such as "300s", "1.5h" or "2h45m". Valid time units are:
    "ns", "us" (or "??s"), "ms", "s", "m", "h".`).Default("1s"),
	},

	Run: func(req cmds.Request, res cmds.Response) {
		nd, err := req.InvocContext().GetNode()
		if err != nil {
			res.SetError(err, cmds.ErrNormal)
			return
		}

		// Must be online!
		if !nd.OnlineMode() {
			res.SetError(errNotOnline, cmds.ErrClient)
			return
		}

		pstr, pfound, err := req.Option("peer").String()
		if err != nil {
			res.SetError(err, cmds.ErrNormal)
			return
		}

		tstr, tfound, err := req.Option("proto").String()
		if err != nil {
			res.SetError(err, cmds.ErrNormal)
			return
		}
		if pfound && tfound {
			res.SetError(errors.New("please only specify peer OR protocol"), cmds.ErrClient)
			return
		}

		var pid peer.ID
		if pfound {
			checkpid, err := peer.IDB58Decode(pstr)
			if err != nil {
				res.SetError(err, cmds.ErrNormal)
				return
			}
			pid = checkpid
		}

		timeS, _, err := req.Option("interval").String()
		if err != nil {
			res.SetError(err, cmds.ErrNormal)
			return
		}
		interval, err := time.ParseDuration(timeS)
		if err != nil {
			res.SetError(err, cmds.ErrNormal)
			return
		}

		doPoll, _, err := req.Option("poll").Bool()
		if err != nil {
			res.SetError(err, cmds.ErrNormal)
			return
		}

		out := make(chan interface{})
		res.SetOutput((<-chan interface{})(out))

		go func() {
			defer close(out)
			for {
				if pfound {
					stats := nd.Reporter.GetBandwidthForPeer(pid)
					out <- &stats
				} else if tfound {
					protoId := protocol.ID(tstr)
					stats := nd.Reporter.GetBandwidthForProtocol(protoId)
					out <- &stats
				} else {
					totals := nd.Reporter.GetBandwidthTotals()
					out <- &totals
				}
				if !doPoll {
					return
				}
				select {
				case <-time.After(interval):
				case <-req.Context().Done():
					return
				}
			}
		}()
	},
	Type: metrics.Stats{},
	Marshalers: cmds.MarshalerMap{
		cmds.Text: func(res cmds.Response) (io.Reader, error) {
			outCh, ok := res.Output().(<-chan interface{})
			if !ok {
				return nil, u.ErrCast()
			}

			polling, _, err := res.Request().Option("poll").Bool()
			if err != nil {
				return nil, err
			}

			first := true
			marshal := func(v interface{}) (io.Reader, error) {
				bs, ok := v.(*metrics.Stats)
				if !ok {
					return nil, u.ErrCast()
				}
				out := new(bytes.Buffer)
				if !polling {
					printStats(out, bs)
				} else {
					if first {
						fmt.Fprintln(out, "Total Up\t Total Down\t Rate Up\t Rate Down")
						first = false
					}
					fmt.Fprint(out, "\r")
					fmt.Fprintf(out, "%s \t\t", humanize.Bytes(uint64(bs.TotalOut)))
					fmt.Fprintf(out, " %s \t\t", humanize.Bytes(uint64(bs.TotalIn)))
					fmt.Fprintf(out, " %s/s   \t", humanize.Bytes(uint64(bs.RateOut)))
					fmt.Fprintf(out, " %s/s     ", humanize.Bytes(uint64(bs.RateIn)))
				}
				return out, nil

			}

			return &cmds.ChannelMarshaler{
				Channel:   outCh,
				Marshaler: marshal,
				Res:       res,
			}, nil
		},
	},
}

func printStats(out io.Writer, bs *metrics.Stats) {
	fmt.Fprintln(out, "Bandwidth")
	fmt.Fprintf(out, "TotalIn: %s\n", humanize.Bytes(uint64(bs.TotalIn)))
	fmt.Fprintf(out, "TotalOut: %s\n", humanize.Bytes(uint64(bs.TotalOut)))
	fmt.Fprintf(out, "RateIn: %s/s\n", humanize.Bytes(uint64(bs.RateIn)))
	fmt.Fprintf(out, "RateOut: %s/s\n", humanize.Bytes(uint64(bs.RateOut)))
}
