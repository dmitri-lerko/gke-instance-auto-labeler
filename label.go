package label

import (
	"autolabel/logging"
	"context"
	"fmt"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
	"os"
	"strings"
	"sync"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/pubsub/v1"
)

type Config struct {
	Projects []string
	Labels   map[string]map[string]string
}

func GKEInstanceAutoLabeler(ctx context.Context, _ pubsub.PubsubMessage) error {
	if err := setLabelsOnInstances(ctx); err != nil {
		logging.Logger.Fatal(err.Error())
	}
	logging.Logger.Info("execution successfully completed")

	return nil
}

func setLabelsOnInstances(ctx context.Context) error {
	f, err := os.Open("./configuration.yaml")
	if err != nil {
		return err
	}

	config := &Config{}
	if err := yaml.NewDecoder(f).Decode(config); err != nil {
		return err
	}

	logging.Logger.Info("configuration retrieved")

	computeService, err := compute.NewService(ctx)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	for _, project := range config.Projects {
		wg.Add(1)
		go evaluateProject(ctx, *config, project, *computeService, &wg)
	}
	wg.Wait()

	return nil
}

func evaluateProject(ctx context.Context, config Config, project string, computeService compute.Service, wg *sync.WaitGroup) {
	defer wg.Done()

	// Only check/set labels on running instances
	filters := []string{
		"status = RUNNING",
	}

	logging.Logger.Infof("listing zones in project %s", project)

	zoneListCall := computeService.Zones.List(project)
	zoneList, err := zoneListCall.Do()
	if err != nil {
		logging.Logger.Debugf("Could not list zones in project %s Skipping...", project)
		return
	}

	for _, zone := range zoneList.Items {
		instanceListCall := computeService.Instances.List(project, zone.Name)
		instanceListCall.Filter(strings.Join(filters[:], " "))

		logging.Logger.Infof("Retrieving a list of instances in zone %s", zone)

		instanceList, err := instanceListCall.Do()
		if err != nil {
			logging.Logger.Errorf("Could not get a list of instances in project %s zone %s", project, zone.Name)
			return
		}

		for _, instance := range instanceList.Items {
			for key, labels := range config.Labels { // Order not specified

				if strings.Contains(instance.Name, key) {

					if mapIsSubsetOfMap(instance.Labels, labels) {
						logging.Logger.Debugf("Correct labels are already set on %s Skipping...", instance.Name)
						continue
					}

					zone := string([]rune(instance.Zone)[strings.LastIndex(instance.Zone, "/")+1:])

					for labelKey, labelValue := range labels {
						instance.Labels[labelKey] = labelValue
					}
					wg.Add(2)
					go reconcileComputeLabels(ctx, wg, computeService, *instance, labels, project, zone)
					go reconcileDiskLabels(ctx, wg, computeService, *instance, labels, project, zone)
				}
			}
		}
	}
}

// Check that map is a subset of another map and values are equal
func mapIsSubsetOfMap(superset map[string]string, subset map[string]string) bool {
	for key, value := range subset {
		if value != superset[key] {
			logging.Logger.Infof("Map %s is not a subset %s", subset, superset)
			return false
		}
	}
	return true
}

// When instance labels have different values to provided labels, use provided values
func reconcileComputeLabels(ctx context.Context, wg *sync.WaitGroup, computeService compute.Service, instance compute.Instance, labels map[string]string, project, zone string) {
	defer wg.Done()

	logging.Logger.Debugf("setting labels on instance %s in project: %s, zone: %s", instance.Name, project, zone)

	rb := &compute.InstancesSetLabelsRequest{
		LabelFingerprint: instance.LabelFingerprint,
		Labels:           instance.Labels,
	}

	_, err := computeService.Instances.SetLabels(project, zone, instance.Name, rb).Context(ctx).Do()
	if err != nil {
		logging.Logger.Error(errors.Wrapf(err, "Error setting labels on %s", instance.Name).Error())
		return
	}
	logging.Logger.Debugf("Set labels %s on instance %s in project %s", labels, instance.Name, project)
}

// Only labels disks that match instance name. GKE does not allow to create multiple peristent disks per instance.
func reconcileDiskLabels(ctx context.Context, wg *sync.WaitGroup, computeService compute.Service, instance compute.Instance, labels map[string]string, project, zone string) {
	defer wg.Done()

	logging.Logger.Debugf("setting labels on instance disks %s in project: %s, zone: %s", instance.Name, project, zone)

	resp, err := computeService.Disks.Get(project, zone, instance.Name).Context(ctx).Do()
	if err != nil {
		logging.Logger.Error(errors.Wrapf(err, fmt.Sprintf("Error getting Disk labels for %s", instance.Name)).Error())
		return
	}

	for labelKey, labelValue := range labels {
		resp.Labels[labelKey] = labelValue
	}

	rb := &compute.ZoneSetLabelsRequest{
		LabelFingerprint: resp.LabelFingerprint,
		Labels:           resp.Labels,
	}

	_, err = computeService.Disks.SetLabels(project, zone, instance.Name, rb).Context(ctx).Do()
	if err != nil {
		logging.Logger.Error(errors.Wrapf(err, "Error setting labels Disk on %s", instance.Name).Error())
		return
	}
}
