package cluster

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"github.com/holisticode/swarm/internal/build"
	"github.com/holisticode/swarm/simulation"
	"github.com/holisticode/swarm/testutil"
)

var (
	nodes = flag.Int("nodes", 20, "number of nodes to create")
)

func init() {
	testutil.Init()
}

func TestCluster(t *testing.T) {
	nodeCount := *nodes

	// Test exec adapter
	t.Run("exec", func(t *testing.T) {
		execPath := "../../../build/bin/swarm"

		if _, err := os.Stat(execPath); err != nil {
			if os.IsNotExist(err) {
				t.Skip("swarm binary not found. build it before running the test")
			}
		}

		tmpdir, err := ioutil.TempDir("", "test-sim-exec")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpdir)
		adapter, err := simulation.NewExecAdapter(simulation.ExecAdapterConfig{
			ExecutablePath:    execPath,
			BaseDataDirectory: tmpdir,
		})
		if err != nil {
			t.Fatalf("could not create exec adapter: %v", err)
		}
		startSimulation(t, adapter, nodeCount)
	})

	// Test docker adapter
	t.Run("docker", func(t *testing.T) {
		if env := build.Env(); env.Name == "local" {
			t.Skip("skip locally")
		}

		config := simulation.DefaultDockerAdapterConfig()
		if !simulation.IsDockerAvailable(config.DaemonAddr) {
			t.Skip("docker is not available, skipping test")
		}
		config.DockerImage = "holisticode/swarm:edge"
		adapter, err := simulation.NewDockerAdapter(config)
		if err != nil {
			t.Fatalf("could not create docker adapter: %v", err)
		}
		startSimulation(t, adapter, nodeCount)
	})

	// Test kubernetes adapter
	t.Run("kubernetes", func(t *testing.T) {
		if env := build.Env(); env.Name == "local" {
			t.Skip("skip locally")
		}

		config := simulation.DefaultKubernetesAdapterConfig()
		if !simulation.IsKubernetesAvailable(config.KubeConfigPath) {
			t.Skip("kubernetes is not available, skipping test")
		}
		config.Namespace = "simulation-test"
		config.DockerImage = "holisticode/swarm:edge"
		adapter, err := simulation.NewKubernetesAdapter(config)
		if err != nil {
			t.Fatalf("could not create kubernetes adapter: %v", err)
		}
		startSimulation(t, adapter, nodeCount)
	})
}

func startSimulation(t *testing.T, adapter simulation.Adapter, count int) {
	sim := simulation.NewSimulation(adapter)

	defer sim.StopAll()

	// Common args used by all nodes
	commonArgs := []string{
		"--bzznetworkid", "599",
	}

	// Start a cluster with 'count' nodes and a bootnode
	nodes, err := sim.CreateClusterWithBootnode("test", count, commonArgs)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for all nodes to be considered healthy
	err = sim.WaitForHealthyNetwork()
	if err != nil {
		t.Errorf("Failed to get healthy network: %v", err)
	}

	// Check hive output on the first node
	client, err := sim.RPCClient(nodes[0].Info().ID)
	if err != nil {
		t.Errorf("Failed to get rpc client: %v", err)
	}

	var hive string
	err = client.Call(&hive, "bzz_hive")
	if err != nil {
		t.Errorf("could not get hive info: %v", err)
	}

	snap, err := sim.Snapshot()
	if err != nil {
		t.Error(err)
	}

	b, err := json.Marshal(snap)
	if err != nil {
		t.Error(err)
	}
	fmt.Println(string(b))

	fmt.Println(hive)
}
