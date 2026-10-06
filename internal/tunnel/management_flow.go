package tunnel

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func flowSnapshotPaths(dir string) (flows, seal, ack string) {
	return filepath.Join(dir, "flows.json"), filepath.Join(dir, "flowseal.json"), filepath.Join(dir, "flow-ack.json")
}

// writeFlowSeal записывает однократный снимок кортежей, связанный с неизменяемым manifest.
func writeFlowSeal(dir, id, manifestHash string, p Plan, flows []ManagementFlow) (FlowSeal, error) {
	var empty FlowSeal
	if flows == nil {
		flows = []ManagementFlow{}
	}
	if p.ID != id || manifestHash == "" {
		return empty, errors.New("Seal не привязан к исходной транзакции")
	}
	peers := make(map[string]bool, len(p.Peers))
	for _, peer := range p.Peers {
		if ip := net.ParseIP(peer); ip != nil {
			peers[ip.String()] = true
		}
	}
	seen := make(map[ManagementFlow]bool, len(flows))
	for i := range flows {
		f := &flows[i]
		local, remote := net.ParseIP(f.LocalIP), net.ParseIP(f.RemoteIP)
		if local == nil || remote == nil || !peers[remote.String()] || (f.LocalPort != p.SSHPort && f.LocalPort != p.UIPort) || f.RemotePort < 1 || f.RemotePort > 65535 || (local.To4() == nil) != (remote.To4() == nil) || seen[*f] {
			return empty, errors.New("Management flow не прошёл проверку seal")
		}
		f.LocalIP, f.RemoteIP = local.String(), remote.String()
		seen[*f] = true
	}
	sort.Slice(flows, func(i, j int) bool {
		if flows[i].LocalIP != flows[j].LocalIP {
			return flows[i].LocalIP < flows[j].LocalIP
		}
		if flows[i].RemoteIP != flows[j].RemoteIP {
			return flows[i].RemoteIP < flows[j].RemoteIP
		}
		if flows[i].LocalPort != flows[j].LocalPort {
			return flows[i].LocalPort < flows[j].LocalPort
		}
		return flows[i].RemotePort < flows[j].RemotePort
	})
	flowsRaw, err := json.MarshalIndent(flows, "", "  ")
	if err != nil {
		return empty, err
	}
	seal := FlowSeal{ID: id, ManifestHash: manifestHash, FlowHash: digest(flowsRaw)}
	sealRaw, err := json.MarshalIndent(seal, "", "  ")
	if err != nil {
		return empty, err
	}
	flowsPath, sealPath, _ := flowSnapshotPaths(dir)
	if err = durableCreate(flowsPath, flowsRaw); err != nil {
		return empty, fmt.Errorf("flows.json уже существует или недоступен: %w", err)
	}
	if err = durableCreate(sealPath, sealRaw); err != nil {
		return empty, err
	}
	return seal, nil
}

func loadFlowSeal(dir, id, manifestHash string, p Plan) (FlowSeal, []ManagementFlow, error) {
	var seal FlowSeal
	var flows []ManagementFlow
	flowsPath, sealPath, _ := flowSnapshotPaths(dir)
	flowsRaw, err := os.ReadFile(flowsPath)
	if err != nil {
		return seal, nil, err
	}
	sealRaw, err := os.ReadFile(sealPath)
	if err != nil {
		return seal, nil, err
	}
	if err = json.Unmarshal(sealRaw, &seal); err != nil {
		return seal, nil, err
	}
	if seal.ID != id || seal.ManifestHash != manifestHash || seal.FlowHash != digest(flowsRaw) {
		return seal, nil, errors.New("Flow seal не совпал с manifest или snapshot")
	}
	if err = json.Unmarshal(flowsRaw, &flows); err != nil {
		return seal, nil, err
	}
	// Validate the decoded list without mutating the durable bytes.
	copyFlows := make([]ManagementFlow, len(flows))
	copy(copyFlows, flows)
	validated, err := writeFlowSealValidation(id, manifestHash, p, copyFlows)
	if err != nil || validated.FlowHash != seal.FlowHash || digest(mustMarshalFlows(copyFlows)) != seal.FlowHash {
		return seal, nil, errors.New("Flow snapshot не прошёл проверку содержимого")
	}
	return seal, flows, nil
}

func writeFlowSealValidation(id, manifestHash string, p Plan, flows []ManagementFlow) (FlowSeal, error) {
	if p.ID != id || manifestHash == "" {
		return FlowSeal{}, errors.New("Flow seal имеет неверную привязку")
	}
	peers := make(map[string]bool, len(p.Peers))
	for _, peer := range p.Peers {
		if ip := net.ParseIP(peer); ip != nil {
			peers[ip.String()] = true
		}
	}
	seen := map[ManagementFlow]bool{}
	for i := range flows {
		f := &flows[i]
		local, remote := net.ParseIP(f.LocalIP), net.ParseIP(f.RemoteIP)
		if local == nil || remote == nil || !peers[remote.String()] || (f.LocalPort != p.SSHPort && f.LocalPort != p.UIPort) || f.RemotePort < 1 || f.RemotePort > 65535 || (local.To4() == nil) != (remote.To4() == nil) || seen[*f] {
			return FlowSeal{}, errors.New("Management flow некорректен")
		}
		f.LocalIP, f.RemoteIP = local.String(), remote.String()
		seen[*f] = true
	}
	sort.Slice(flows, func(i, j int) bool {
		if flows[i].LocalIP != flows[j].LocalIP {
			return flows[i].LocalIP < flows[j].LocalIP
		}
		if flows[i].RemoteIP != flows[j].RemoteIP {
			return flows[i].RemoteIP < flows[j].RemoteIP
		}
		if flows[i].LocalPort != flows[j].LocalPort {
			return flows[i].LocalPort < flows[j].LocalPort
		}
		return flows[i].RemotePort < flows[j].RemotePort
	})
	return FlowSeal{ID: id, ManifestHash: manifestHash, FlowHash: digest(mustMarshalFlows(flows))}, nil
}

func mustMarshalFlows(flows []ManagementFlow) []byte {
	b, _ := json.MarshalIndent(flows, "", "  ")
	return b
}

func validateFlowAck(dir, id, manifestHash string, p Plan) (FlowSeal, error) {
	seal, _, err := loadFlowSeal(dir, id, manifestHash, p)
	if err != nil {
		return seal, err
	}
	_, _, ackPath := flowSnapshotPaths(dir)
	data, err := os.ReadFile(ackPath)
	if err != nil {
		return seal, err
	}
	var ack FlowAck
	if err = json.Unmarshal(data, &ack); err != nil {
		return seal, err
	}
	if ack.ID != seal.ID || ack.ManifestHash != seal.ManifestHash || ack.FlowHash != seal.FlowHash || ack.State != "sealed" {
		return seal, errors.New("Watchdog flow ACK не совпал с seal")
	}
	return seal, nil
}

func waitFlowAck(dir, id, manifestHash string, p Plan, deadline time.Time) (FlowSeal, error) {
	for i := 0; i < 50; i++ {
		seal, err := validateFlowAck(dir, id, manifestHash, p)
		if err == nil {
			return seal, nil
		}
		if time.Now().After(deadline) {
			return FlowSeal{}, errors.New("Истёк lease до подтверждения management flow seal")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return FlowSeal{}, errors.New("Watchdog не подтвердил management flow seal")
}
