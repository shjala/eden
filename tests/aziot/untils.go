package aziot

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/go-tpm/tpmutil"
	etpm "github.com/lf-edge/eve/pkg/pillar/evetpm"
)

func readPublicKey(handle tpmutil.Handle) ([]byte, error) {
	// unfortunaly we can't used SWTPM socket directly, it is blocked becuse
	// qemu is using it, so we have to use ssh and tpm2-tools
	tpmToolsPath := "/containers/services/vtpm/lower/usr/bin/tpm2"
	tpmToolsLibPath := "/containers/services/vtpm/lower/usr/local/lib"
	command := fmt.Sprintf("LD_LIBRARY_PATH=%s %s readpublic -Q -c 0x%x -o pub.pub", tpmToolsLibPath, tpmToolsPath, handle)
	_, err := eveNode.EveRunCommand(command)
	if err != nil {
		return nil, err
	}

	out, err := eveNode.EveReadFile("pub.pub")
	if err != nil {
		return nil, err
	}

	err = eveNode.EveDeleteFile("pub.pub")
	if err != nil {
		return nil, err
	}

	return out, nil
}

func readEveEndorsmentKey() (string, string, error) {
	pub, err := readPublicKey(etpm.TpmEKHdl)
	if err != nil {
		return "", "", err
	}

	hash := sha256.Sum256(pub)
	hashHex := hex.EncodeToString(hash[:])

	return base64.StdEncoding.EncodeToString(pub), hashHex, nil
}

func getAzureIoTServicesStatus(output string) (map[string]string, error) {
	// this what is being parse:
	//$ sudo iotedge system status
	//System services:
	//aziot-edged             Running
	//aziot-identityd         Down - activating
	//aziot-keyd              Ready
	//aziot-certd             Ready
	//aziot-tpmd              Running

	// Flag to indicate if we are in the "System services" section
	inSystemServices := false
	services := make(map[string]string, 0)

	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()

		// Detect when we are in the "System services" section
		if strings.Contains(line, "System services:") {
			inSystemServices = true
			continue
		}

		// Exit the loop when we are out of the "System services" section
		if inSystemServices && strings.TrimSpace(line) == "" {
			break
		}

		if inSystemServices {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				services[parts[0]] = strings.Join(parts[1:], " ")
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return services, nil
}
