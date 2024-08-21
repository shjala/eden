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

// RemoteNode is a struct that holds the information about the remote node
type RemoteNode struct {
	Controller *openevec.OpenEVEC
	Edgenode   *device.Ctx
	Tc         *projects.TestContext
	IP         string
	SSHPort    string
	SSHUser    string
	SSHPass    string
}

// GetOpenEVEC returns the OpenEVEC controller
func GetOpenEVEC() *openevec.OpenEVEC {
	edenConfigEnv := os.Getenv(defaults.DefaultConfigEnv)
	configName := utils.GetConfig(edenConfigEnv)

	viperCfg, err := openevec.FromViper(configName, "debug")
	if err != nil {
		return nil
	}

	return openevec.CreateOpenEVEC(viperCfg)
}

// CreateRemoteNode creates a new RemoteNode struct
func CreateRemoteNode(node *device.Ctx, tc *projects.TestContext) *RemoteNode {
	evec := GetOpenEVEC()
	if evec == nil {
		return nil
	}

	return &RemoteNode{Controller: evec, Edgenode: node, Tc: tc, IP: "", SSHPort: "22"}
}

// RunEveCommand runs a command on the EVE node
func (node *RemoteNode) RunEveCommand(command string) ([]byte, error) {
	realStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	os.Stdout = w

	// unfortunately, we can't capture command return value from SSHEve
	err = node.Controller.SSHEve(command)

	os.Stdout = realStdout
	w.Close()

	if err != nil {
		return nil, err
	}

	out, _ := io.ReadAll(r)
	return out, nil
}

// FileExists checks if a file exists on EVE node
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

// ReadFile reads a file from EVE node
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

// DeleteFile deletes a file from EVE node
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

// WaitForAppStart waits for an app to start on the EVE node
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

// WaitForSSH waits for the SSH connection to be established to the app VM that
// is running on the EVE node
func (node *RemoteNode) WaitForSSH(timeoutSeconds uint) error {
	start := time.Now()
	for {
		_, err := node.AppSSHExec("echo")
		if err == nil {
			return nil
		}

		if time.Since(start) > time.Duration(timeoutSeconds)*time.Second {
			return fmt.Errorf("timeout waiting for SSH connection")
		}

		time.Sleep(5 * time.Second)
	}
}

// StopAndRemoveApp stops and removes an app from the EVE node
func (node *RemoteNode) StopAndRemoveApp(appName string) error {
	if err := node.Controller.PodStop(appName); err != nil {
		return err
	}

	if _, err := node.Controller.PodDelete(appName, true); err != nil {
		return err
	}

	return nil
}

// GetNodeIP gets the IP address of the app running the EVE node
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

		node.IP = ip.To4().String()
		return nil
	}

	node.IP = node.Edgenode.GetRemoteAddr()
	return nil
}

// GetAppState gets the state of an app running on the EVE node
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

// AppSSHExec executes a command on the app VM running on the EVE node
func (node *RemoteNode) AppSSHExec(command string) (string, error) {
	host := fmt.Sprintf("%s:%s", node.IP, node.SSHPort)

	config := &ssh.ClientConfig{
		User: node.SSHUser,
		Auth: []ssh.AuthMethod{
			ssh.Password(node.SSHPass),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}
	client, err := ssh.Dial("tcp", host, config)
	if err != nil {
		return "", fmt.Errorf("failed to dial: %s", err)
	}
	defer client.Close()

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

// AppSCPCopy copies a file from the local machine to the app VM running on the EVE node
func (node *RemoteNode) AppSCPCopy(localFile, remoteFile string) error {
	host := fmt.Sprintf("%s:%s", node.IP, node.SSHPort)

	config := &ssh.ClientConfig{
		User: node.SSHUser,
		Auth: []ssh.AuthMethod{
			ssh.Password(node.SSHPass),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}
	client, err := ssh.Dial("tcp", host, config)
	if err != nil {
		return fmt.Errorf("failed to dial: %s", err)
	}
	defer client.Close()

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
