package vcom

import (
	"os"
	"strings"
	"testing"
	"time"

	tk "github.com/lf-edge/eden/pkg/evetestkit"
	log "github.com/sirupsen/logrus"
)

var eveNode *tk.EveNode

const (
	sshPort = "8027"
	appLink = "https://cloud-images.ubuntu.com/releases/22.04/release/ubuntu-22.04-server-cloudimg-amd64.img"
)

func TestMain(m *testing.M) {
	log.Println("VCOM Test started")
	defer log.Println("VCOM Test finished")

	node, err := tk.InitilizeTest(m, "vcomlink")
	if err != nil {
		log.Fatalf("Failed to initialize test: %v", err)
	}

	eveNode = node
	res := m.Run()
	os.Exit(res)
}

func TestVcomLink(t *testing.T) {
	log.Println("TestVcomLink started")
	defer log.Println("TestVcomLink finished")

	if !eveNode.IsTpmEnabled() {
		t.Skip("TPM is enabled, skipping test")
	}

	t.Log("Checking if vcomlink is running on EVE")
	stat, err := eveNode.EveRunCommand("eve exec pillar ss -l --vsock")
	// vcomlink listens on port 2000 and host cid is 2.
	// this is hacky way to check it is running, but it works ¯\_(ツ)_/¯
	if !strings.Contains(string(stat), "2:2000") {
		t.Fatalf("vcomlink is not running %v", err)
	}

	appName := tk.GetRandomAppName("vcom-")
	pc := tk.GetDefaultVmConfig(appName, tk.DefaultCloudConfig, []string{sshPort + ":22"})
	err = eveNode.DeployVm(appLink, pc)
	if err != nil {
		t.Fatalf("Failed to deploy app: %v", err)
	}
	defer func() {
		err = eveNode.StopAndRemoveApp(appName)
		if err != nil {
			log.Errorf("Failed to stop and remove app: %v", err)
		}
	}()

	// wait for app to deploy
	time.Sleep(10 * time.Second)

	// wait 5 minutes for the app to start
	t.Logf("Waiting for app %s to start...", appName)
	err = eveNode.WaitForAppRunningState(appName, 60*5)
	if err != nil {
		t.Fatalf("Failed to wait for app to start: %v", err)
	}

	// add app to the list so we can use it
	eveNode.AddApp(appName, tk.DefaultSSHUser, tk.DefaultSSHPass, sshPort)

	t.Logf("Waiting for ssh to be ready...")
	err = eveNode.WaitForSSH(appName, 60*5)
	if err != nil {
		t.Fatalf("Failed to wait for ssh: %v", err)
	}
	t.Logf("SSH connection established")

	t.Log("Installing socat on the vm")
	_, err = eveNode.SSHExec(appName, "sudo apt-get -y install socat")
	if err != nil {
		t.Fatalf("Failed install socat on the vm: %v", err)
	}

	// send a request to vcomlink via vsock to get the host's TPM EK
	command := "echo '{\"channel\":2,\"request\":1}' | socat - VSOCK-CONNECT:2:2000"
	out, err := eveNode.SSHExec(appName, command)
	if err != nil {
		t.Fatalf("Failed to communicate with host via vsock: %v", err)
	}

	// XXX : fix this by importing vcom from eve and unmrashal the output
	// to TpmResponseEk
	if strings.Contains(string(out), "error") {
		t.Fatalf("Failed to communicate with host via vsock: %v", out)
	}
	t.Log("Successfully communicated from VM to vcomlink (host) via vsock")
}
