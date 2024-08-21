package vcom

import (
	"os"
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	sshPort            = "8027"
	vmUser             = "ubuntu"
	vmPass             = "passw0rd"
	testScriptBasePath = "/home/ubuntu/"
	cloudConfig        = "#cloud-config\npassword: passw0rd\nchpasswd: { expire: False }\nssh_pwauth: True\n"
	appLink            = "https://cloud-images.ubuntu.com/releases/22.04/release/ubuntu-22.04-server-cloudimg-amd64.img"
)

func TestMain(m *testing.M) {
	log.Println("VCOM Test started")
	res := initilizeTest(m)

	log.Println("VCOM Test finished")
	os.Exit(res)
}

func TestVcomLink(t *testing.T) {
	log.Println("TestVcomLink started")
	defer log.Println("TestVcomLink finished")

	if !isTpmEnabled() {
		t.Skip("TPM is enabled, skipping test")
	}

	stat, err := rnode.RunEveCommand("eve exec pillar ss -l --vsock")
	// vcomlink listens on port 2000 and host cid is 2.
	// this is hacky way to check it is running, but it works ¯\_(ツ)_/¯
	if !strings.Contains(string(stat), "2:2000") {
		t.Fatalf("vcomlink is not running %v", err)
	}

	appname = getAppName("vcom-")
	publishedPorts := sshPort + ":22"
	err = deployApp(appLink, appname, cloudConfig, []string{publishedPorts})
	if err != nil {
		t.Fatalf("Failed to deploy app: %v", err)
	}
	defer func() {
		err = rnode.StopAndRemoveApp(appname)
		if err != nil {
			log.Errorf("Failed to stop and remove app: %v", err)
		}
	}()

	// wait for app to deploy
	time.Sleep(10 * time.Second)

	// wait 5 minutes for the app to start
	t.Logf("Waiting for app %s to start...", appname)
	err = rnode.WaitForAppStart(appname, 60*5)
	if err != nil {
		t.Fatalf("Failed to wait for app to start: %v", err)
	}

	rnode.SSHUser = vmUser
	rnode.SSHPass = vmPass
	rnode.SSHPort = sshPort
	err = rnode.GetNodeIP()
	if err != nil {
		t.Fatalf("Failed to get node IP: %v", err)
	}

	t.Logf("Waiting for ssh to be ready...")
	err = rnode.WaitForSSH(60 * 5)
	if err != nil {
		t.Fatalf("Failed to wait for ssh: %v", err)
	}
	t.Logf("SSH connection established")

	_, err = rnode.AppSSHExec("sudo apt-get -y install socat")
	if err != nil {
		t.Fatalf("Failed install socat on the vm: %v", err)
	}

	// send a request to vcomlink via vsock to get the host's TPM EK
	command := "echo '{\"channel\":2,\"request\":1}' | socat - VSOCK-CONNECT:2:2000"
	out, err := rnode.AppSSHExec(command)
	if err != nil {
		t.Fatalf("Failed to communicate with host via vsock: %v", err)
	}

	// XXX : fix this by importing vcom from eve and unmrashal the output
	// to TpmResponseEk
	if strings.Contains(string(out), "error") {
		t.Fatalf("Failed to communicate with host via vsock: %v", out)
	}
}
