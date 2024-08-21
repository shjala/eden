package remote

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/lf-edge/eden/pkg/controller"
	"github.com/lf-edge/eden/pkg/defaults"
	"github.com/lf-edge/eden/pkg/device"
	"github.com/lf-edge/eden/pkg/eve"
	"github.com/lf-edge/eden/pkg/openevec"
	"github.com/lf-edge/eden/pkg/projects"
	"github.com/lf-edge/eden/pkg/utils"
	"github.com/tmc/scp"
	"golang.org/x/crypto/ssh"
)

type RemoteNode struct {
	Controler *openevec.OpenEVEC
	Edgenode  *device.Ctx
	Tc        *projects.TestContext
	Ip        string
	SshPort   string
	SshUser   string
	SshPass   string
}

func GetOpenEVEC() *openevec.OpenEVEC {
	edenConfigEnv := os.Getenv(defaults.DefaultConfigEnv)
	configName := utils.GetConfig(edenConfigEnv)

	viperCfg, err := openevec.FromViper(configName, "debug")
	if err != nil {
		return nil
	}

	return openevec.CreateOpenEVEC(viperCfg)
}

func CreateRemoteNode(node *device.Ctx, tc *projects.TestContext) *RemoteNode {
	evec := GetOpenEVEC()
	if evec == nil {
		return nil
	}

	return &RemoteNode{Controler: evec, Edgenode: node, Tc: tc, Ip: "", SshPort: "22"}
}

func (node *RemoteNode) RunEveCommand(command string) ([]byte, error) {
	realStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	os.Stdout = w

	// unfortunately, we can't capture command return value from SSHEve
	err = node.Controler.SSHEve(command)

	os.Stdout = realStdout
	w.Close()

	if err != nil {
		return nil, err
	}

	out, _ := io.ReadAll(r)
	return out, nil
}

func (node *RemoteNode) FileExists(fileName string) (bool, error) {
	command := fmt.Sprintf("if stat \"%s\"; then echo \"1\"; else echo \"0\"; fi", fileName)
	out, err := node.RunEveCommand(command)
	if err != nil {
		return false, err
	}

	if strings.TrimSpace(string(out)) == "0" {
		return false, nil
	}

	return true, nil
}

func (node *RemoteNode) ReadFile(fileName string) ([]byte, error) {
	exist, err := node.FileExists(fileName)
	if err != nil {
		return nil, err
	}

	if !exist {
		return nil, fmt.Errorf("file %s does not exist", fileName)
	}

	command := fmt.Sprintf("cat %s", fileName)
	return node.RunEveCommand(command)
}

func (node *RemoteNode) DeleteFile(fileName string) error {
	exist, err := node.FileExists(fileName)
	if err != nil {
		return err
	}

	if !exist {
		return nil
	}

	command := fmt.Sprintf("rm %s", fileName)
	_, err = node.RunEveCommand(command)
	return err
}

func (node *RemoteNode) WaitForAppStart(appName string, timeoutSeconds uint) error {
	start := time.Now()
	for {
		state, err := node.GetAppState(appName)
		if err != nil {
			return err
		}

		if strings.ToLower(state) == "running" {
			return nil
		}

		if time.Since(start) > time.Duration(timeoutSeconds)*time.Second {
			return fmt.Errorf("timeout waiting for app %s to start", appName)
		}

		time.Sleep(5 * time.Second)
	}
}

func (node *RemoteNode) WaitForSsh(timeoutSeconds uint) error {
	start := time.Now()
	for {
		_, err := node.AppSshExec("echo")
		if err == nil {
			return nil
		}

		if time.Since(start) > time.Duration(timeoutSeconds)*time.Second {
			return fmt.Errorf("timeout waiting for SSH connection")
		}

		time.Sleep(5 * time.Second)
	}
}

func (node *RemoteNode) StopAndRemoveApp(appName string) error {
	if err := node.Controler.PodStop(appName); err != nil {
		return err
	}

	if _, err := node.Controler.PodDelete(appName, true); err != nil {
		return err
	}

	return nil
}

func (node *RemoteNode) GetNodeIP() error {
	if node.Edgenode.GetRemoteAddr() == "" {
		eveIPCIDR, err := node.Tc.GetState(node.Edgenode).LookUp("Dinfo.Network[0].IPAddrs[0]")
		if err != nil {
			return err
		}

		ip := net.ParseIP(eveIPCIDR.String())
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("failed to parse IP address: %s", eveIPCIDR.String())
		}

		node.Ip = ip.To4().String()
		return nil
	}

	node.Ip = node.Edgenode.GetRemoteAddr()
	return nil
}

func (node *RemoteNode) GetAppState(appName string) (string, error) {
	ctrl, err := controller.CloudPrepare()
	if err != nil {
		return "", fmt.Errorf("fail in CloudPrepare: %w", err)
	}

	state := eve.Init(ctrl, node.Edgenode)
	if err := ctrl.InfoLastCallback(node.Edgenode.GetID(), nil, state.InfoCallback()); err != nil {
		return "", fmt.Errorf("fail in get InfoLastCallback: %w", err)
	}
	if err := ctrl.MetricLastCallback(node.Edgenode.GetID(), nil, state.MetricCallback()); err != nil {
		return "", fmt.Errorf("fail in get MetricLastCallback: %w", err)
	}
	appStatesSlice := make([]*eve.AppInstState, 0, len(state.Applications()))
	appStatesSlice = append(appStatesSlice, state.Applications()...)
	for _, app := range appStatesSlice {
		if app.Name == appName {
			return app.EVEState, nil
		}
	}

	return "", fmt.Errorf("app %s not found", appName)
}

func (node *RemoteNode) AppSshExec(command string) (string, error) {
	host := fmt.Sprintf("%s:%s", node.Ip, node.SshPort) // Include port if necessary (default is 22)

	config := &ssh.ClientConfig{
		User: node.SshUser,
		Auth: []ssh.AuthMethod{
			ssh.Password(node.SshPass),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}
	client, err := ssh.Dial("tcp", host, config)
	if err != nil {
		return "", fmt.Errorf("failed to dial: %s", err)
	}
	defer client.Close()

	// Create a session
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %s", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput(command)
	if err != nil {
		return "", fmt.Errorf("failed to run command: %s", err)
	}

	return string(output), nil
}

func (node *RemoteNode) AppScpCopy(localFile, remoteFile string) error {
	host := fmt.Sprintf("%s:%s", node.Ip, node.SshPort)

	config := &ssh.ClientConfig{
		User: node.SshUser,
		Auth: []ssh.AuthMethod{
			ssh.Password(node.SshPass),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}
	client, err := ssh.Dial("tcp", host, config)
	if err != nil {
		return fmt.Errorf("failed to dial: %s", err)
	}
	defer client.Close()

	// Create a session
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %s", err)
	}
	defer session.Close()

	err = scp.CopyPath(localFile, remoteFile, session)
	if err != nil {
		return nil
	}

	return nil
}
