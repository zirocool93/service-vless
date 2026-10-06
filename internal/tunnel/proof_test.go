package tunnel

import "testing"

func TestHostNamespaceProofBoundToNamespaceAndBoot(t *testing.T) {
	current := hostNamespaceProof{Namespace: "net:[4026531840]", BootID: "boot-a"}
	if !hostNamespaceProofMatches(current, current) {
		t.Fatal("точное доказательство отклонено")
	}
	for _, stored := range []hostNamespaceProof{
		{},
		{Namespace: current.Namespace, BootID: "boot-b"},
		{Namespace: "net:[4026532000]", BootID: current.BootID},
	} {
		if hostNamespaceProofMatches(stored, current) {
			t.Fatalf("чужое или устаревшее доказательство принято: %+v", stored)
		}
	}
}
