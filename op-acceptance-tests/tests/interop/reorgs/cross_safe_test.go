package reorgs

import (
	"fmt"
	"testing"
	"time"

	"github.com/ethereum-optimism/optimism/op-devstack/devtest"
	"github.com/ethereum-optimism/optimism/op-devstack/presets"
	"github.com/ethereum-optimism/optimism/op-devstack/stack"
	"github.com/ethereum-optimism/optimism/op-devstack/stack/match"
	"github.com/ethereum-optimism/optimism/op-test-sequencer/sequencer/seqtypes"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestReorgCrossSafeHead starts an interop chain with an op-test-sequencer, which takes control over sequencing the L1 chain,
// introduces a reorg on the L1 block, which the cross-safe head is pointing to, and then checks that the L2 chain is reorged
func TestReorgCrossSafeHead(gt *testing.T) {
	// heavy wip -- at the moment this test is not checking for cross-safe head, we are just reorging the L1 chain head without regard to the cross-safe head
	t := devtest.SerialT(gt)
	ctx := t.Ctx()

	// sys := presets.NewSimpleInterop(t)
	sys := presets.NewMinimal(t)
	l := sys.Log

	cl := sys.L1Network.Escape().L1CLNode(match.FirstL1CL)
	el := sys.L1Network.Escape().L1ELNode(match.FirstL1EL)

	sys.L1Network.WaitForBlock()
	sys.L1Network.WaitForBlock()

	// reorg the L1 chain
	{
		sys.ControlPlane.FakePoSState(cl.ID(), stack.Stop)

		sequenceL1Block(t, sys, common.Hash{})

		sys.L2Chain.WaitForBlock()
		sys.L2Chain.WaitForBlock()
		sys.L2Chain.WaitForBlock()
		sys.L2Chain.WaitForBlock()

		headL1, err := el.EthClient().InfoByLabel(ctx, "latest")
		require.NoError(t, err)

		// print the chains before waiting
		sys.L2Chain.PrintChain()
		sys.L1Network.PrintChain()

		l.Info("sequence an L1 block with the same parent as the latest head", "number", headL1.NumberU64(), "hash", headL1.Hash(), "parent", headL1.ParentHash())
		sequenceL1Block(t, sys, headL1.ParentHash())

		sys.ControlPlane.FakePoSState(cl.ID(), stack.Start)
	}

	time.Sleep(60 * time.Second)

	// print the chains after the reorg
	sys.L2Chain.PrintChain()
	sys.L1Network.PrintChain()
}

func TestL1Reorg(gt *testing.T) {
	t := devtest.SerialT(gt)
	ctx := t.Ctx()

	sys := presets.NewMinimal(t)
	l := sys.Log

	cl := sys.L1Network.Escape().L1CLNode(match.FirstL1CL)
	el := sys.L1Network.Escape().L1ELNode(match.FirstL1EL)

	sys.ControlPlane.FakePoSState(cl.ID(), stack.Stop)

	var parent common.Hash
	for range 2 {
		l.Info("sequence an L1 blog")
		sequenceL1Block(t, sys, parent)
	}

	head, err := el.EthClient().InfoByLabel(ctx, "latest")
	require.NoError(t, err)

	sys.L1Network.PrintChain()

	l.Info("sequence an L1 blog with the same parent as the latest head", "number", head.NumberU64(), "hash", head.Hash(), "parent", head.ParentHash())

	sequenceL1Block(t, sys, head.ParentHash())

	sys.L1Network.PrintChain()

	sys.ControlPlane.FakePoSState(cl.ID(), stack.Start)

	sys.L1Network.WaitForBlock()

	nhead, err := el.EthClient().InfoByNumber(ctx, head.NumberU64())
	require.NoError(t, err)

	t.Require().Equal(head.NumberU64(), nhead.NumberU64(), fmt.Sprintf("head and nhead block numbers should be different, head: %d, nhead: %d", head.NumberU64(), nhead.NumberU64()))
	t.Require().NotEqual(head.Hash(), nhead.Hash(), fmt.Sprintf("head and nhead should be different, head: %s, nhead: %s", head.Hash(), nhead.Hash()))

	sys.L1Network.PrintChain()
}

func sequenceL1Block(t devtest.T, sys *presets.Minimal, parent common.Hash) {
	l1s := sys.Sequencer.Escape().IndividualAPI(sys.L1Network.ChainID())

	err := l1s.New(t.Ctx(), seqtypes.BuildOpts{
		Parent: parent,
	})
	require.NoError(t, err)

	err = l1s.Next(t.Ctx())
	require.NoError(t, err)
}
