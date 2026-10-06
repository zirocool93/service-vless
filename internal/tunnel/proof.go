package tunnel

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const hostNamespaceProofFile = "host-netns.json"

type hostNamespaceProof struct {
	Namespace string `json:"namespace"`
	BootID    string `json:"boot_id"`
}

func currentHostNamespaceProof() (hostNamespaceProof, error) {
	var proof hostNamespaceProof
	ns, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return proof, err
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return proof, err
	}
	proof.Namespace = ns
	proof.BootID = strings.TrimSpace(string(boot))
	if proof.Namespace == "" || proof.BootID == "" {
		return hostNamespaceProof{}, errors.New("Пустое доказательство network namespace")
	}
	return proof, nil
}

func invalidateHostNamespaceProof(root string) error {
	path := filepath.Join(root, hostNamespaceProofFile)
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if os.IsNotExist(err) {
		return nil
	}
	dir, err := os.Open(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func saveHostNamespaceProof(root string) error {
	proof, err := currentHostNamespaceProof()
	if err != nil {
		return err
	}
	return saveJSON(filepath.Join(root, hostNamespaceProofFile), proof)
}

func verifyHostNamespaceProof(root string) error {
	b, err := os.ReadFile(filepath.Join(root, hostNamespaceProofFile))
	if err != nil {
		return errors.New("Recovery не подтвердил host network namespace")
	}
	var stored hostNamespaceProof
	if json.Unmarshal(b, &stored) != nil {
		return errors.New("Доказательство host network namespace повреждено")
	}
	current, err := currentHostNamespaceProof()
	if err != nil {
		return err
	}
	if !hostNamespaceProofMatches(stored, current) {
		return errors.New("Backend запущен не в подтверждённом host network namespace или после другой загрузки")
	}
	return nil
}

func hostNamespaceProofMatches(stored, current hostNamespaceProof) bool {
	return stored.Namespace != "" && stored.BootID != "" && stored == current
}
