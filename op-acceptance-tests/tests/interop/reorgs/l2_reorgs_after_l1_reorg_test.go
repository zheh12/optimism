package reorgs

import (
	"testing"
	"time"

	"github.com/ethereum-optimism/optimism/op-devstack/devtest"
	"github.com/ethereum-optimism/optimism/op-devstack/dsl"
	"github.com/ethereum-optimism/optimism/op-devstack/presets"
	"github.com/ethereum-optimism/optimism/op-devstack/stack"
	"github.com/ethereum-optimism/optimism/op-devstack/stack/match"
	"github.com/ethereum-optimism/optimism/op-service/apis"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-test-sequencer/sequencer/seqtypes"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

type postChecksFunc func(t devtest.T, l2EL *dsl.L2ELNode, preSyncStatus eth.SupervisorSyncStatus)

func TestL2ReorgAfterL1Reorg(gt *testing.T) {
	gt.Run("unsafe reorg", func(gt *testing.T) {
		post := func(t devtest.T, l2EL *dsl.L2ELNode, pss eth.SupervisorSyncStatus) {
			cid := l2EL.ChainID()
			require.True(t, l2EL.IsCanonical(pss.Chains[cid].CrossSafe), "Previous cross-safe block (%s) should still be canonical", pss.Chains[cid].CrossSafe)
			require.True(t, l2EL.IsCanonical(pss.Chains[cid].LocalSafe), "Previous local-safe block (%s) should still be canonical", pss.Chains[cid].LocalSafe)
			require.False(t, l2EL.IsCanonical(pss.Chains[cid].LocalUnsafe.ID()), "Previous unsafe block (%s) should have been reorged", pss.Chains[cid].LocalUnsafe.ID())
		}
		testL2ReorgAfterL1Reorg(gt, 3, post)
	})

	gt.Run("local-safe and cross-safe reorgs", func(gt *testing.T) {
		post := func(t devtest.T, l2EL *dsl.L2ELNode, pss eth.SupervisorSyncStatus) {
			cid := l2EL.ChainID()
			require.False(t, l2EL.IsCanonical(pss.Chains[cid].CrossSafe), "Previous cross-safe block (%s) should have been reorged", pss.Chains[cid].CrossSafe)
			require.False(t, l2EL.IsCanonical(pss.Chains[cid].LocalSafe), "Previous local-safe block (%s) should have been reorged", pss.Chains[cid].LocalSafe)
			require.False(t, l2EL.IsCanonical(pss.Chains[cid].LocalUnsafe.ID()), "Previous unsafe block (%s) should have been reorged", pss.Chains[cid].LocalUnsafe.ID())
		}
		testL2ReorgAfterL1Reorg(gt, 10, post)
	})
}

// testL2ReorgAfterL1Reorg tests that the L2 chain reorgs after an L1 reorg, and takes n, number of blocks to reorg, as parameter
// for unsafe reorgs - n must be at least >= confDepth, which is 2 in our test deployments
// for cross-safe reorgs - n must be at least >= safe distance, which is 10 in our test deployments (set in
// op-e2e/e2eutils/geth/geth.go when initialising FakePoS)
// pre- and post-checks are sanity checks to ensure that the blocks we expected to be reorged were indeed reorged or not
func testL2ReorgAfterL1Reorg(gt *testing.T, n int, postChecks postChecksFunc) {
	t := devtest.SerialT(gt)

	// sys := presets.NewSimpleInterop(t)
	sys := presets.NewMultiSupervisorInterop(t)
	ts := sys.TestSequencer.Escape().ControlAPI(sys.L1Network.ChainID())

	cl := sys.L1Network.Escape().L1CLNode(match.FirstL1CL)

	sys.L1Network.WaitForBlock()

	sys.ControlPlane.FakePoSState(cl.ID(), stack.Stop)

	// sequence a few L1 and L2 blocks
	for range n + 1 {
		sequenceL1Block(t, ts, common.Hash{})

		sys.L2ChainA.WaitForBlock()
		sys.L2ChainA.WaitForBlock()
	}

	// select a divergence block to reorg from
	var divergence eth.L1BlockRef
	{
		tip := sys.L1EL.BlockRefByLabel(eth.Unsafe)
		require.Greater(t, tip.Number, uint64(n), "n is larger than L1 tip")

		divergence = sys.L1EL.BlockRefByNumber(tip.Number - uint64(n))
	}

	// print the chains before sequencing an alternative L1 block
	sys.L2ChainA.PrintChain(sys.L2CLA)
	sys.L2ChainA.PrintChain(sys.L2CLA2)
	sys.L1Network.PrintChain()

	// record pre-reorg sync statuses from both supervisor nodes
	preSyncStatus := sys.Supervisor.FetchSyncStatus()
	preSyncStatusSecondary := sys.SupervisorSecondary.FetchSyncStatus()

	// reorg the L1 chain -- sequence an alternative L1 block from divergence block parent
	sequenceL1Block(t, ts, divergence.ParentHash)

	// continue building on the alternative L1 chain
	sys.ControlPlane.FakePoSState(cl.ID(), stack.Start)

	// verify for both ELs, since one is verifier-node and the other is sequencer-node
	for _, el := range []*dsl.L2ELNode{sys.L2ELA, sys.L2ELA2} {
		// verify that latest chain A unsafe is not referencing a reorged L1 block (through the L1Origin field)
		require.Eventually(t, func() bool {
			unsafe := el.BlockRefByLabel(eth.Unsafe)

			block := sys.L1EL.BlockRefByNumber(unsafe.L1Origin.Number)

			sys.Log.Info("current unsafe ref", "tip", unsafe, "tip.L1Origin", unsafe.L1Origin, "L1 block", block)

			// print the chains so we have information to debug if the test fails
			sys.L2ChainA.PrintChain(sys.L2CLA)
			sys.L1Network.PrintChain()

			return block.Hash == unsafe.L1Origin.Hash
		}, 120*time.Second, 15*time.Second, "L1 block origin hash for tip should match hash of block on L1 at that number. If not, it means there was a reorg, and L2 blocks L1Origin field is referencing a reorged block.")

		// confirm all L1Origin fields point to canonical blocks (for both clients on chain A)
		ref := el.BlockRefByLabel(eth.Unsafe)
		for i := ref.Number; i > 0; i-- {
			ref := el.BlockRefByNumber(i)
			require.True(t, sys.L1EL.IsCanonical(ref.L1Origin), "L1 block origin should be canonical")
		}
	}

	// post reorg test validations and checks
	{
		sys.Log.Info("Running post reorg test validations and checks for sequencer-node")
		postChecks(t, sys.L2ELA, preSyncStatus)

		sys.Log.Info("Running post reorg test validations and checks for verifier-node")
		postChecks(t, sys.L2ELA2, preSyncStatusSecondary)
	}
}

func sequenceL1Block(t devtest.T, ts apis.TestSequencerControlAPI, parent common.Hash) {
	require.NoError(t, ts.New(t.Ctx(), seqtypes.BuildOpts{Parent: parent}))
	require.NoError(t, ts.Next(t.Ctx()))
}
