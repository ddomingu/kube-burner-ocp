// Copyright 2026 The Kube-burner Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package workloads

import (
	"fmt"
	"time"

	"github.com/cloud-bulldozer/go-commons/v2/ssh"
	"github.com/cloud-bulldozer/go-commons/v2/virtctl"
	"github.com/kube-burner/kube-burner/v2/pkg/workloads"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

const (
	VirtDVScaleDensitySSHKeyFileName = "ssh"
	VirtDVScaleDensityTmpDirPattern  = "kube-burner-dv-scale-density-*"
	virtDVScaleDensityTestName       = "virt-dv-scale-density"
)

var (
	virtDVScaleDensityNamespaceLabelSelector = fmt.Sprintf("%s=%s", kubeBurnerTestNameLabelKey, virtDVScaleDensityTestName)
)

// NewVirtDVScaleDensity holds the virt-dv-scale-density workload
func NewVirtDVScaleDensity(wh *workloads.WorkloadHelper) *cobra.Command {
	var storageClassName string
	var volumeSnapshotClassName string
	var sshKeyPairPath string
	var useSnapshot bool
	var namespaces int
	var vmsPerNamespace int
	var dataVolumeCount int
	var vmImageURL string
	var dataVolumeSize string
	var dataVolumeSizeAdditional string
	var vmCPU int
	var vmMemory string
	var volumeAccessMode string
	var jobIterationDelay time.Duration
	var testNamespaceBaseName string
	var metricsProfiles []string
	var cleanupOnly bool
	var cleanup bool
	var rc int

	cmd := &cobra.Command{
		Use:          virtDVScaleDensityTestName,
		Short:        "Runs virt-dv-scale-density workload",
		SilenceUsage: true,
		PreRun: func(cmd *cobra.Command, args []string) {
			if cleanupOnly {
				return
			}

			if _, ok := accessModeTranslator[volumeAccessMode]; !ok {
				log.Fatalf("Unsupported access mode - %s", volumeAccessMode)
			}

			if !virtctl.IsInstalled() {
				log.Fatalf("Failed to run virtctl. Check that it is installed, in PATH and working")
			}

			storageClassName, volumeSnapshotClassName = getStorageAndSnapshotClasses(storageClassName, useSnapshot, cmd.Flags().Lookup("use-snapshot").Changed)
		},
		Run: func(cmd *cobra.Command, args []string) {
			if cleanupOnly {
				log.Infof("Cleaning up all the resources from the previous run")
				cleanupTestNamespaces(cmd.Context(), virtDVScaleDensityNamespaceLabelSelector)
				return
			}

			privateKeyPath, publicKeyPath, err := ssh.GenerateSSHKeyPair(sshKeyPairPath, VirtDVScaleDensityTmpDirPattern, VirtDVScaleDensitySSHKeyFileName)
			if err != nil {
				log.Fatalf("Failed to generate SSH keys for the test - %v", err)
			}

			wh.SummaryMetadata["OCPVirtualizationVersion"], err = wh.MetadataAgent.GetOCPVirtualizationVersion()
			if err != nil {
				log.Warnf("Failed to get OCP Virtualization version: %v", err)
			}

			totalVMs := namespaces * vmsPerNamespace
			totalPVCs := totalVMs * (1 + dataVolumeCount) // root + data volumes

			log.Infof("Running virt-dv-scale-density with %d namespaces, %d VMs per namespace", namespaces, vmsPerNamespace)
			log.Infof("Total VMs: %d, Total PVCs: %d (including %d data volumes per VM)", totalVMs, totalPVCs, dataVolumeCount)
			log.Infof("Using Storage Class [%s], VolumeSnapshotClass [%s]", storageClassName, volumeSnapshotClassName)
			log.Infof("VM Image URL: %s", vmImageURL)
			log.Infof("Use Snapshot: %t", useSnapshot)

			AdditionalVars["privateKey"] = privateKeyPath
			AdditionalVars["publicKey"] = publicKeyPath
			AdditionalVars["storageClassName"] = storageClassName
			AdditionalVars["volumeSnapshotClassName"] = volumeSnapshotClassName
			AdditionalVars["accessMode"] = accessModeTranslator[volumeAccessMode]
			AdditionalVars["useSnapshot"] = useSnapshot
			AdditionalVars["vmsPerNamespace"] = vmsPerNamespace
			AdditionalVars["vmImageURL"] = vmImageURL
			AdditionalVars["dataVolumeSize"] = dataVolumeSize
			AdditionalVars["dataVolumeSizeAdditional"] = dataVolumeSizeAdditional
			AdditionalVars["vmCPU"] = vmCPU
			AdditionalVars["vmMemory"] = vmMemory
			AdditionalVars["dataVolumeCounters"] = generateLoopCounterSlice(dataVolumeCount, 1)

			setMetrics(cmd, metricsProfiles)

			// Loop through namespaces
			for counter := 0; counter < namespaces; counter++ {
				currentNamespace := fmt.Sprintf("%s-%d", testNamespaceBaseName, counter)
				log.Infof("Running namespace %d/%d: %s", counter+1, namespaces, currentNamespace)

				AdditionalVars["counter"] = counter
				AdditionalVars["testNamespace"] = currentNamespace

				rc = RunWorkload(cmd, wh, cmd.Name()+".yml")
				if rc != 0 {
					log.Errorf("virt-dv-scale-density failed in namespace %s", currentNamespace)
					break
				}

				// Add delay between namespaces (except after the last one)
				if counter < namespaces-1 {
					log.Infof("Waiting %s before processing next namespace", jobIterationDelay)
					time.Sleep(jobIterationDelay)
				}
			}

			if cleanup {
				log.Infof("Cleaning up all the resources from the current run")
				cleanupTestNamespaces(cmd.Context(), virtDVScaleDensityNamespaceLabelSelector)
			}
		},
		PostRun: func(cmd *cobra.Command, args []string) {
			if rc != 0 {
				log.Errorf("virt-dv-scale-density failed with return code %d", rc)
			}
		},
	}

	cmd.Flags().StringVar(&storageClassName, "storage-class", "", "Name of the Storage Class to test")
	cmd.Flags().StringVar(&sshKeyPairPath, "ssh-key-path", "", "Path to save the generated SSH keys - default to a temporary location")
	cmd.Flags().BoolVar(&useSnapshot, "use-snapshot", true, "Clone from snapshot (true) or direct PVC clone (false)")
	cmd.Flags().IntVar(&namespaces, "namespaces", 2, "Number of namespaces to create")
	cmd.Flags().IntVar(&vmsPerNamespace, "vms-per-namespace", 10, "Number of VMs to create per namespace")
	cmd.Flags().IntVar(&dataVolumeCount, "data-volume-count", 0, "Number of additional data volumes per VM (default: 0)")
	cmd.Flags().StringVar(&vmImageURL, "vm-image-url", "https://dl.fedoraproject.org/pub/fedora/linux/releases/43/Cloud/x86_64/images/Fedora-Cloud-Base-Generic-43-1.6.x86_64.qcow2", "HTTP URL of the source image for base DataVolume")
	cmd.Flags().StringVar(&dataVolumeSize, "datavolume-size", "10Gi", "Size of the root DataVolume")
	cmd.Flags().StringVar(&dataVolumeSizeAdditional, "data-volume-size", "1Gi", "Size of each additional data volume")
	cmd.Flags().IntVar(&vmCPU, "vm-cpu", 1, "Number of CPU cores for each VM")
	cmd.Flags().StringVar(&vmMemory, "vm-memory", "1G", "Memory allocation for each VM")
	cmd.Flags().StringVar(&volumeAccessMode, "access-mode", "RWX", "Access mode for the created volumes - RO, RWO, RWX")
	cmd.Flags().DurationVar(&jobIterationDelay, "job-iteration-delay", 1*time.Minute, "Delay between namespace iterations")
	cmd.Flags().StringVarP(&testNamespaceBaseName, "namespace", "n", virtDVScaleDensityTestName, "Base namespace name for the test")
	cmd.Flags().StringSliceVar(&metricsProfiles, "metrics-profile", []string{"metrics-aggregated.yml"}, "Comma separated list of metrics profiles to use")
	cmd.Flags().BoolVar(&cleanupOnly, "cleanup-only", false, "Only cleanup the resources created by the previous run. Do not run the test.")
	cmd.Flags().BoolVar(&cleanup, "cleanup", false, "Cleanup the resources created by the test.")

	return cmd
}
