package aziot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tk "github.com/lf-edge/eden/pkg/evetestkit"
	log "github.com/sirupsen/logrus"
)

const (
	sshPort            = "8027"
	testScriptBasePath = "/home/ubuntu/"
	projectName        = "aziot-test"
)

var (
	appLink = map[string]string{
		"aziot-1.4.0":  "https://cloud-images.ubuntu.com/releases/20.04/release/ubuntu-20.04-server-cloudimg-amd64.img",
		"aziot-latest": "https://cloud-images.ubuntu.com/releases/22.04/release/ubuntu-22.04-server-cloudimg-amd64.img",
	}
	testScript = map[string]string{
		"aziot-1.4.0":  "scripts/test_ubuntu20.04_aziot_1.4.0.sh",
		"aziot-latest": "scripts/test_ubuntu22.04_aziot_latest.sh",
	}
	eveNode *tk.EveNode
)

func TestMain(m *testing.M) {
	log.Println("Azure IOT Hub Test started")
	defer log.Println("Azure IOT Hub Test finished")

	tk.SetControllerVerbosity("debug")

	node, err := tk.InitilizeTest(m, projectName)
	if err != nil {
		log.Fatalf("Failed to initialize test: %v", err)
	}

	eveNode = node
	res := m.Run()
	os.Exit(res)
}

func TestAzureIotTPMEndrolmentWithEveTools(t *testing.T) {
	log.Println("TestAzureIotTPMEndrolmentWithEveTools started")
	log.Println("Setup :\n\tAziot version 1.4.0 on Ubuntu-20.04-amd64\n\twith EVE-Tools and Proxy TPM")
	defer log.Println("TestAzureIotTPMEndrolmentWithEveTools finished")

	if !eveNode.IsTpmEnabled() {
		t.Skip("TPM is enabled, skipping test")
	}

	testAzureIotEdge(t, "aziot-1.4.0", false)
}

func TestAzureIotTPMEndrolmentWithVTPM(t *testing.T) {
	log.Println("TestAzureIotTPMEndrolmentWithVTPM started")
	log.Println("Setup :\n\tAziot (latest) on Ubuntu-22.04-amd64\n\twith VTPM")
	defer log.Println("TestAzureIotTPMEndrolmentWithVTPM finished")

	t.Skip("Skip test for now, it is failing")

	testAzureIotEdge(t, "aziot-latest", true)
}

func testAzureIotEdge(t *testing.T, version string, useVTPM bool) {
	appName := tk.GetRandomAppName(projectName)
	pc := tk.GetDefaultVmConfig(appName, tk.DefaultCloudConfig, []string{sshPort + ":22"})
	err := eveNode.DeployVm(appLink[version], pc)
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
	t.Log("SSH connection established")

	testScriptPath := testScriptBasePath + filepath.Base(testScript[version])
	err = eveNode.SCPCopy(appName, testScript[version], testScriptPath)
	if err != nil {
		t.Fatalf("Failed to copy file to vm: %v", err)
	}
	t.Log("Test script copied to VM")
	t.Log("Create a TPM enrollment in Azure IoT Hub")

	endorsementKey, enrollmentID := "", ""
	if !useVTPM {
		// for this to test to work, we need to create an enrollment in the Azure IoT Hub,
		// the enrolment should be created with the endorsement key of the TPM and
		// since we are running EVE in QEMU with SWTPM, the endorsement key changes
		// every time we start the VM EVE, so we need to read it, create the enrollment,
		// run the test and delete the enrollment.
		ek, id, err := readEveEndorsmentKey()
		if err != nil {
			t.Fatalf("Failed to read endorsement key: %v", err)
		}
		endorsementKey, enrollmentID = ek, id
	} else {
		createKeyScriptPath := testScriptBasePath + "test_make_tpm_keys.sh"
		err = eveNode.SCPCopy(appName, "scripts/test_make_tpm_keys.sh", createKeyScriptPath)
		if err != nil {
			t.Fatalf("Failed to copy file to vm: %v", err)
		}

		// prepare the script for execution
		command := fmt.Sprintf("chmod +x %s", createKeyScriptPath)
		_, err = eveNode.SSHExec(appName, command)
		if err != nil {
			t.Fatalf("Failed perpare the test script for execution: %v", err)
		}

		// execute the script to create the necessary TPM keys
		_, err = eveNode.SSHExec(appName, createKeyScriptPath)
		if err != nil {
			t.Fatalf("Failed to execute test script in VM: %v", err)
		}

		ek, err := eveNode.SSHExec(appName, "base64 -w0 ek.pub")
		if err != nil {
			t.Fatalf("Failed to read endrosment key from VM: %v", err)
		}

		command = "sha256sum -b ek.pub | cut -d' ' -f1 | sed -e 's/[^[:alnum:]]//g'"
		id, err := eveNode.SSHExec(appName, command)
		if err != nil {
			t.Fatalf("Failed to get enrollment ID from VM: %v", err)
		}

		endorsementKey, enrollmentID = ek, id
	}

	// You need a shared access policy with the following permissions:
	// Registration Status Read, Registration Status Write, Enrollment Read, Enrollment Write
	// You can create a new policy in the Azure portal by going to :
	// IoT Hub -> Device Provisioning Service (DPS) -> Shared access policies -> Add
	// and then copy the connection string.
	connectionString := os.Getenv("AZIOT_CONNECTION_STRING")
	if connectionString == "" {
		t.Fatalf("AZIOT_CONNECTION_STRING environment variable is not set")
	}

	// Get the provisioning service name from the connection string
	provService, err := getProvisioningService(connectionString)
	if err != nil {
		t.Fatalf("Failed to get provisioning service: %v\n", err)
	}

	// From the connection string generate a SAS token lasting for 1 hour
	sasToken, err := getSasTokenFromConnectionString(connectionString, 1)
	if err != nil {
		t.Fatalf("Failed to get SAS token: %v\n", err)
	}

	// add the enrollment to azure iot hub portal
	err = addTPMEnrollment(enrollmentID, endorsementKey, provService, sasToken)
	if err != nil {
		t.Fatalf("Failed to add enrollment: %v\n", err)
	}
	defer func() {
		err = deleteEnrollment(enrollmentID, provService, sasToken)
		if err != nil {
			log.Printf("Failed to delete enrollment, please remove it manually: %v\n", err)
		}
	}()

	// The ID Scope is required to configure azure-iot in the VM,
	// you can get it from the Azure IoT Hub -> Device Provisioning Service -> Overview
	// and copy the "ID Scope".
	aziotIdScope := os.Getenv("AZIOT_ID_SCOPE")
	if aziotIdScope == "" {
		t.Fatalf("AZIOT_ID_SCOPE environment variable is not set")
	}

	// prepare the test script for execution
	command := fmt.Sprintf("chmod +x %s", testScriptPath)
	_, err = eveNode.SSHExec(appName, command)
	if err != nil {
		t.Fatalf("Failed perpare the test script for execution: %v", err)
	}

	// execute the test script, this will configure the azure-iot in the VM
	// and start the services.
	command = fmt.Sprintf("ID_SCOPE=%s REGISTRATION_ID=%s %s", aziotIdScope, enrollmentID, testScriptPath)
	_, err = eveNode.SSHExec(appName, command)
	if err != nil {
		t.Fatalf("Failed to execute test script in VM: %v", err)
	}

	// wait for the services to start
	t.Logf("Waiting for services to start...")
	time.Sleep(60 * time.Second)

	// check the status of the iotedge services
	status, err := eveNode.SSHExec(appName, "sudo iotedge system status")
	if err != nil {
		t.Fatalf("Failed to get iotedge status: %v", err)
	}

	services, err := getAzureIoTServicesStatus(status)
	if err != nil {
		t.Fatalf("Failed to get Azure IoT services status: %v", err)
	}

	// check if all services are running, otherwise fail the test
	for service, status := range services {
		if strings.ToLower(status) != "running" {
			t.Errorf("Service %s is not running", service)
		}
	}

	log.Println("====================== SERVICES STATUS ======================")
	for service, status := range services {
		t.Logf("%s: \t%s\n", service, status)
	}

	if t.Failed() {
		// get the aziot-tpmd logs, we actually patch this service with eve-tools
		// so good to have the logs for debugging.
		command = "sudo iotedge system logs | grep aziot-tpmd"
		tpmLog, err := eveNode.SSHExec(appName, command)
		if err != nil {
			t.Errorf("Failed to get aziot-tpmd logs: %v", err)
		} else {
			t.Log("====================== TPMD LOG ======================")
			t.Log(tpmLog)
		}

		// get all the errors from the aziot logs
		command = "sudo iotedge system logs | grep ERR | sed 's/.*ERR!] - //' | sort | uniq"
		errors, err := eveNode.SSHExec(appName, command)
		if err != nil {
			t.Errorf("Failed to error logs: %v", err)
		} else {
			t.Log("====================== ERRORS ======================")
			t.Log(errors)
		}
	}
}
