package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/jsonmergepatch"
	clusterapi "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// PatchCluster makes patch request to the MachineSet object.
func PatchCluster(ctx context.Context, client client.Client, cur, mod *clusterapi.Cluster) error {
	curJSON, err := json.Marshal(cur)
	if err != nil {
		return fmt.Errorf("failed to serialize current MachineSet object: %s", err)
	}

	modJSON, err := json.Marshal(mod)
	if err != nil {
		return fmt.Errorf("failed to serialize modified MachineSet object: %s", err)
	}
	patch, err := jsonmergepatch.CreateThreeWayJSONMergePatch(curJSON, modJSON, curJSON)
	if err != nil {
		return fmt.Errorf("failed to create 2-way merge patch: %s", err)
	}
	if len(patch) == 0 || string(patch) == "{}" {
		return nil
	}
	patchObj := runtimeclient.RawPatch(types.MergePatchType, patch)
	// client patch machineSet replicas
	err = client.Patch(ctx, cur, patchObj)
	if err != nil {
		return fmt.Errorf("failed to patch MachineSet object %s/%s: %s", cur.Namespace, cur.Name, err)
	}

	return nil
}
