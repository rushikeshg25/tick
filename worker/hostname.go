package worker

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// FromHostname returns a Static allocator whose node ID is the ordinal suffix
// of the host's name.
//
// A Kubernetes StatefulSet names its pods with a stable trailing ordinal, so
// "ingest-7" yields node ID 7. That makes the identity survive rescheduling
// without any coordination, which is why it is worth special-casing.
//
// It is only correct for workloads with stable ordinals. A Deployment's pod
// names end in a random suffix, and a ReplicaSet reuses ordinals freely, so
// neither is safe here.
func FromHostname() (Allocator, error) {
	name, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("worker: reading hostname: %w", err)
	}
	id, err := OrdinalFromName(name)
	if err != nil {
		return nil, err
	}
	return Static(id), nil
}

// OrdinalFromName extracts the trailing ordinal from a StatefulSet pod name.
func OrdinalFromName(name string) (uint16, error) {
	i := strings.LastIndexByte(name, '-')
	if i < 0 || i == len(name)-1 {
		return 0, fmt.Errorf("worker: %q has no trailing ordinal; only StatefulSet pod names carry one", name)
	}
	n, err := strconv.ParseUint(name[i+1:], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("worker: %q has no trailing ordinal; only StatefulSet pod names carry one", name)
	}
	if n > maxNodeID {
		return 0, fmt.Errorf("worker: ordinal %d in %q exceeds the maximum node id %d", n, name, maxNodeID)
	}
	return uint16(n), nil
}
