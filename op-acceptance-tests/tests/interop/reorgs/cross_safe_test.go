package reorgs

import (
	"fmt"
	"testing"
	"time"

	"github.com/ethereum-optimism/optimism/op-devstack/devtest"
	"github.com/ethereum-optimism/optimism/op-devstack/dsl"
	"github.com/ethereum-optimism/optimism/op-devstack/presets"
	"github.com/ethereum-optimism/optimism/op-devstack/stack"
	"github.com/ethereum-optimism/optimism/op-devstack/stack/match"
	"github.com/ethereum-optimism/optimism/op-service/apis"
	"github.com/ethereum-optimism/optimism/op-test-sequencer/sequencer/seqtypes"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestReorgL1(gt *testing.T) {
	t := devtest.SerialT(gt)

	t.Run("interop", func(t devtest.T) {
		reorgL1(t, "interop")
	})
	t.Run("minimal", func(t devtest.T) {
		reorgL1(t, "minimal")
	})
}

func reorgL1(t devtest.T, preset string) {
	ctx := t.Ctx()

	var l2 *dsl.L2Network
	var l1 *dsl.L1Network
	var sequencer apis.SequencerIndividualAPI
	var cp stack.ControlPlane
	if preset == "interop" {
		sys := presets.NewSimpleInterop(t)
		l2 = sys.L2ChainA
		l1 = sys.L1Network
		sequencer = sys.Sequencer.Escape().IndividualAPI(l1.ChainID())
		cp = sys.ControlPlane
	} else if preset == "minimal" {
		sys := presets.NewMinimal(t)
		l2 = sys.L2Chain
		l1 = sys.L1Network
		sequencer = sys.Sequencer.Escape().IndividualAPI(l1.ChainID())
		cp = sys.ControlPlane
	}

	cl := l1.Escape().L1CLNode(match.FirstL1CL)
	el := l1.Escape().L1ELNode(match.FirstL1EL)

	l1.WaitForBlock()
	l1.WaitForBlock()

	// reorg the L1 chain
	{
		cp.FakePoSState(cl.ID(), stack.Stop)

		sequenceL1Block(t, sequencer, common.Hash{})

		l2.WaitForBlock()
		l2.WaitForBlock()
		l2.WaitForBlock()
		l2.WaitForBlock()

		headL1, err := el.EthClient().InfoByLabel(ctx, "latest")
		require.NoError(t, err)

		// print the chains before waiting
		l2.PrintChain()
		l1.PrintChain()

		sequenceL1Block(t, sequencer, headL1.ParentHash())

		cp.FakePoSState(cl.ID(), stack.Start)
	}

	time.Sleep(60 * time.Second)

	// print the chains after the reorg
	l2.PrintChain()
	l1.PrintChain()
}

func sequenceL1Block(t devtest.T, l1s apis.SequencerIndividualAPI, parent common.Hash) {
	err := l1s.New(t.Ctx(), seqtypes.BuildOpts{
		Parent: parent,
	})
	require.NoError(t, err)

	err = l1s.Next(t.Ctx())
	require.NoError(t, err)
}

func TestL1Reorg_simple(gt *testing.T) {
	t := devtest.SerialT(gt)
	ctx := t.Ctx()

	sys := presets.NewSimpleInterop(t)
	// sys := presets.NewMinimal(t)
	l := sys.Log

	l1s := sys.Sequencer.Escape().IndividualAPI(sys.L1Network.ChainID())

	cl := sys.L1Network.Escape().L1CLNode(match.FirstL1CL)
	el := sys.L1Network.Escape().L1ELNode(match.FirstL1EL)

	sys.ControlPlane.FakePoSState(cl.ID(), stack.Stop)

	var parent common.Hash
	for range 2 {
		l.Info("sequence an L1 blog")
		sequenceL1Block(t, l1s, parent)
	}

	head, err := el.EthClient().InfoByLabel(ctx, "latest")
	require.NoError(t, err)

	sys.L1Network.PrintChain()

	l.Info("sequence an L1 blog with the same parent as the latest head", "number", head.NumberU64(), "hash", head.Hash(), "parent", head.ParentHash())

	sequenceL1Block(t, l1s, head.ParentHash())

	sys.L1Network.PrintChain()

	sys.ControlPlane.FakePoSState(cl.ID(), stack.Start)

	sys.L1Network.WaitForBlock()

	nhead, err := el.EthClient().InfoByNumber(ctx, head.NumberU64())
	require.NoError(t, err)

	t.Require().Equal(head.NumberU64(), nhead.NumberU64(), fmt.Sprintf("head and nhead block numbers should be different, head: %d, nhead: %d", head.NumberU64(), nhead.NumberU64()))
	t.Require().NotEqual(head.Hash(), nhead.Hash(), fmt.Sprintf("head and nhead should be different, head: %s, nhead: %s", head.Hash(), nhead.Hash()))

	sys.L1Network.PrintChain()
}
