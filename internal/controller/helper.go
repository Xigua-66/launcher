// Copyright 2022 EasyStack, Inc.
package controller

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	NETATTDEFFINALIZERNAME  = "plan.finalizers.eks.io"
)

const invalidVfIndex = -1

var MANIFESTS_PATH = "./bindata/manifests/cni-config"
var hlog = logf.Log.WithName("sriovnetwork")

// NicIdMap contains supported mapping of IDs with each in the format of:
// Vendor ID, Physical Function Device ID, Virtual Function Device ID
var NicIdMap = []string{}

// NetFilterType Represents the NetFilter tags to be used
type NetFilterType int

const (
	// OpenstackNetworkID network UUID
	OpenstackNetworkID NetFilterType = iota

	SUPPORTED_NIC_ID_CONFIGMAP = "supported-nic-ids"
)


func (e NetFilterType) String() string {
	switch e {
	case OpenstackNetworkID:
		return "openstack/NetworkID"
	default:
		return fmt.Sprintf("%d", int(e))
	}
}

func InitNicIdMap(client *kubernetes.Clientset, namespace string) error {
	cm, err := client.CoreV1().ConfigMaps(namespace).Get(
		context.Background(),
		SUPPORTED_NIC_ID_CONFIGMAP,
		metav1.GetOptions{},
	)
	// if the configmap does not exist, return false
	if err != nil {
		return err
	}
	for _, v := range cm.Data {
		NicIdMap = append(NicIdMap, v)
	}
	return nil
}

func IsSupportedVendor(vendorId string) bool {
	for _, n := range NicIdMap {
		ids := strings.Split(n, " ")
		if vendorId == ids[0] {
			return true
		}
	}
	return false
}

func IsSupportedDevice(deviceId string) bool {
	for _, n := range NicIdMap {
		ids := strings.Split(n, " ")
		if deviceId == ids[1] {
			return true
		}
	}
	return false
}

func IsSupportedModel(vendorId, deviceId string) bool {
	for _, n := range NicIdMap {
		ids := strings.Split(n, " ")
		if vendorId == ids[0] && deviceId == ids[1] {
			return true
		}
	}
	hlog.Info("IsSupportedModel():", "Unsupported model:", "vendorId:", vendorId, "deviceId:", deviceId)
	return false
}

func IsVfSupportedModel(vendorId, deviceId string) bool {
	for _, n := range NicIdMap {
		ids := strings.Split(n, " ")
		if vendorId == ids[0] && deviceId == ids[2] {
			return true
		}
	}
	hlog.Info("IsVfSupportedModel():", "Unsupported VF model:", "vendorId:", vendorId, "deviceId:", deviceId)
	return false
}

func IsEnabledUnsupportedVendor(vendorId string, unsupportedNicIdMap map[string]string) bool {
	for _, n := range unsupportedNicIdMap {
		if IsValidPciString(n) {
			ids := strings.Split(n, " ")
			if vendorId == ids[0] {
				return true
			}
		}
	}
	return false
}

func IsValidPciString(nicIdString string) bool {
	ids := strings.Split(nicIdString, " ")

	if len(ids) != 3 {
		hlog.Info("IsValidPciString(): ", nicIdString)
		return false
	}

	if len(ids[0]) != 4 {
		hlog.Info("IsValidPciString():", "Invalid vendor PciId ", ids[0])
		return false
	}
	if _, err := strconv.ParseInt(ids[0], 16, 32); err != nil {
		hlog.Info("IsValidPciString():", "Invalid vendor PciId ", ids[0])
	}

	if len(ids[1]) != 4 {
		hlog.Info("IsValidPciString():", "Invalid PciId of PF ", ids[1])
		return false
	}
	if _, err := strconv.ParseInt(ids[1], 16, 32); err != nil {
		hlog.Info("IsValidPciString():", "Invalid PciId of PF ", ids[1])
	}

	if len(ids[2]) != 4 {
		hlog.Info("IsValidPciString():", "Invalid PciId of VF ", ids[2])
		return false
	}
	if _, err := strconv.ParseInt(ids[2], 16, 32); err != nil {
		hlog.Info("IsValidPciString():", "Invalid PciId of VF ", ids[2])
	}

	return true
}

func StringInArray(val string, array []string) bool {
	for i := range array {
		if array[i] == val {
			return true
		}
	}
	return false
}

func GetSupportedVfIds() []string {
	var vfIds []string
	for _, n := range NicIdMap {
		ids := strings.Split(n, " ")
		vfId := "0x" + ids[2]
		if !StringInArray(vfId, vfIds) {
			vfIds = append(vfIds, vfId)
		}
	}
	// return a sorted slice so that udev rule is stable
	sort.Slice(vfIds, func(i, j int) bool {
		ip, _ := strconv.ParseInt(vfIds[i], 0, 32)
		jp, _ := strconv.ParseInt(vfIds[j], 0, 32)
		return ip < jp
	})
	return vfIds
}

func GetVfDeviceId(deviceId string) string {
	for _, n := range NicIdMap {
		ids := strings.Split(n, " ")
		if deviceId == ids[1] {
			return ids[2]
		}
	}
	return ""
}

func RemoveString(s string, slice []string) (result []string, found bool) {
	if len(slice) != 0 {
		for _, item := range slice {
			if item == s {
				found = true
				continue
			}
			result = append(result, item)
		}
	}
	return
}

