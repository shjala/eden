package vcom

import (
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/pkg/namesgenerator"
	"github.com/dustin/go-humanize"
	"github.com/lf-edge/eden/pkg/defaults"
	"github.com/lf-edge/eden/pkg/device"
	"github.com/lf-edge/eden/pkg/openevec"
	"github.com/lf-edge/eden/pkg/projects"
	"github.com/lf-edge/eden/pkg/remote"
	"github.com/lf-edge/eden/pkg/tests"
	"github.com/lf-edge/eden/pkg/utils"
	"golang.org/x/exp/rand"
)

var (
	rnode   *remote.RemoteNode
	appname string
)

// TestMain is used to provide setup and teardown for the rest of the
// tests. As part of setup we make sure that context has a slice of
// EVE instances that we can operate on. For any action, if the instance
// is not specified explicitly it is assumed to be the first one in the slice
func initilizeTest(m *testing.M) int {
	var edgenode *device.Ctx
	tests.TestArgsParse()

	tc := projects.NewTestContext()

	projectName := fmt.Sprintf("%s_%s", "TestAzureIoT", time.Now())

	// Registering our own project namespace with controller for easy cleanup
	tc.InitProject(projectName)

	// Create representation of EVE instances (based on the names
	// or UUIDs that were passed in) in the context. This is the first place
	// where we're using zcli-like API:
	for _, node := range tc.GetNodeDescriptions() {
		edgeNode := node.GetEdgeNode(tc)
		if edgeNode == nil {
			// Couldn't find existing edgeNode record in the controller.
			// Need to create it from scratch now:
			// this is modeled after: zcli edge-node create <name>
			// --project=<project> --model=<model> [--title=<title>]
			// ([--edge-node-certificate=<certificate>] |
			// [--onboarding-certificate=<certificate>] |
			// [(--onboarding-key=<key> --serial=<serial-number>)])
			// [--network=<network>...]
			//
			// XXX: not sure if struct (giving us optional fields) would be better
			edgeNode = tc.NewEdgeNode(tc.WithNodeDescription(node), tc.WithCurrentProject())
		} else {
			// make sure to move EdgeNode to the project we created, again
			// this is modeled after zcli edge-node update <name> [--title=<title>]
			// [--lisp-mode=experimental|default] [--project=<project>]
			// [--clear-onboarding-certs] [--config=<key:value>...] [--network=<network>...]
			edgeNode.SetProject(projectName)
		}

		edgenode = edgeNode
		tc.ConfigSync(edgeNode)

		// finally we need to make sure that the edgeNode is in a state that we need
		// it to be, before the test can run -- this could be multiple checks on its
		// status, but for example:
		if edgeNode.GetState() == device.NotOnboarded {
			log.Fatal("Node is not onboarded now")
		}

		// this is a good node -- lets add it to the test context
		tc.AddNode(edgeNode)
	}

	tc.StartTrackingState(false)

	// create a remote node
	rnode = remote.CreateRemoteNode(edgenode, tc)
	if rnode == nil {
		log.Fatal("Can't initlize the remote node")
	}

	// we now have a situation where TestContext has enough EVE nodes known
	// for the rest of the tests to run. So run them:
	return m.Run()
}

func deployApp(appLink, name, metadata string, portPub []string) error {
	var pc openevec.PodConfig

	edenConfigEnv := os.Getenv(defaults.DefaultConfigEnv)
	configName := utils.GetConfig(edenConfigEnv)
	cfg, err := openevec.FromViper(configName, "debug")
	if err != nil {
		return err
	}

	pc.Name = name
	pc.AppMemory = humanize.Bytes(defaults.DefaultAppMem * 1024)
	pc.DiskSize = "4GB"
	pc.VolumeType = "QCOW2"
	pc.Metadata = metadata
	pc.VncPassword = ""
	pc.ImageFormat = "QCOW2"
	pc.Registry = "remote"
	pc.VolumeSize = humanize.IBytes(defaults.DefaultVolumeSize)
	pc.PortPublish = portPub
	pc.VncDisplay = 0
	pc.AppCpus = defaults.DefaultAppCPU
	pc.AppAdapters = nil
	pc.Networks = nil
	pc.ACLOnlyHost = false
	pc.NoHyper = false
	pc.DirectLoad = true
	pc.SftpLoad = false
	pc.Disks = nil
	pc.Mount = nil
	pc.Profiles = nil
	pc.ACL = nil
	pc.Vlans = nil
	pc.OpenStackMetadata = false
	pc.DatastoreOverride = ""
	pc.StartDelay = 0
	pc.PinCpus = false

	if err := rnode.Controler.PodDeploy(appLink, pc, cfg); err != nil {
		return err
	}

	return nil
}

func getAppName(prefix string) string {
	rnd := rand.New(rand.NewSource(uint64(time.Now().UnixNano())))
	return prefix + namesgenerator.GetRandomName(rnd.Intn(1))
}

func isTpmEnabled() bool {
	edenConfigEnv := os.Getenv(defaults.DefaultConfigEnv)
	configName := utils.GetConfig(edenConfigEnv)
	cfg, err := openevec.FromViper(configName, "debug")
	if err != nil {
		return false
	}

	return cfg.Eve.TPM
}
