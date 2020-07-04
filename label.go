package label

import (
	"autolabel/logging"
	"context"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/pubsub/v1"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Projects []string
	Labels   map[string]map[string]string
}

func GKEInstanceAutoLabeler(ctx context.Context, _ pubsub.PubsubMessage) error {
	filename, _ := filepath.Abs("./configuration.yaml")
	yamlFile, err := ioutil.ReadFile(filename)

	if err != nil {
		panic(err)
	}

	var config Config

	err = yaml.Unmarshal(yamlFile, &config)
	if err != nil {
		panic(err)
	}

	err = setLabelsOnInstances(ctx, config)
	if err != nil {
		logging.Logger.Error(err.Error())
		return err
	}

	return nil
}

func setLabelsOnInstances(ctx context.Context, config Config) error {

	computeService, err := compute.NewService(ctx)
	if err != nil {
		fmt.Println("Error", err)
		logging.Logger.Error(fmt.Sprintf("Could not create compute service. Error: %s", err))
		return err
	}

	var wg sync.WaitGroup
	for _, project := range config.Projects {
		wg.Add(1)
		go evaluateProject(ctx, config, project, *computeService, &wg)
	}
	wg.Wait()

	return nil
}

func evaluateProject(ctx context.Context, config Config, project string, computeService compute.Service, wg *sync.WaitGroup) error {
	defer wg.Done()

	// Only check/set labels on running instances
	filters := [...]string{
		"status = RUNNING",
	}

	zoneListCall := computeService.Zones.List(project)
	zoneList, err := zoneListCall.Do()
	if err != nil {
		logging.Logger.Debug(fmt.Sprintf("Could not list zones in project %s Skipping...", project))
		return err
	}

	for _, zone := range zoneList.Items {
		instanceListCall := computeService.Instances.List(project, zone.Name)
		instanceListCall.Filter(strings.Join(filters[:], " "))
		instanceList, err := instanceListCall.Do()
		if err != nil {
			logging.Logger.Error(fmt.Sprintf("Could not get a list of instances in project %s zone %s", project, zone.Name))
			return err
		}

		for _, instance := range instanceList.Items {

			reconcileInstanceLabels(ctx, computeService, *instance, config, project)
		}
	}
	return nil
}

// Check that map is a subset of another map and values are equal
func mapIsSubsetOfMap(superset map[string]string, subset map[string]string) bool {
	for key, value := range subset {
		if value != superset[key] {
			logging.Logger.Info(fmt.Sprintf("Map %s is not a subset %s", subset, superset))
			return false
		}
	}
	return true
}

// When instance labels have different values to provided labels, use provided values
func reconcileInstanceLabels(ctx context.Context, computeService compute.Service, instance compute.Instance, config Config, project string) error {
	for key, labels := range config.Labels { // Order not specified

		if strings.Contains(instance.Name, key) {

			if mapIsSubsetOfMap(instance.Labels, labels) {
				logging.Logger.Debug(fmt.Sprintf("Correct labels are already set on %s Skipping...", instance.Name))
				continue
			}

			zone := string([]rune(instance.Zone)[strings.LastIndex(instance.Zone, "/")+1:])

			for labelKey, labelValue := range labels {
				instance.Labels[labelKey] = labelValue
			}

			rb := &compute.InstancesSetLabelsRequest{
				LabelFingerprint: instance.LabelFingerprint,
				Labels:           instance.Labels,
			}

			_, err := computeService.Instances.SetLabels(project, zone, instance.Name, rb).Context(ctx).Do()
			if err != nil {
				logging.Logger.Error(fmt.Sprintf("Error setting labels on %s", instance.Name))
				fmt.Println(err)
				return err
			}
			logging.Logger.Debug(fmt.Sprintf("Set labels %s on instance %s in project %s", labels, instance.Name, project))

			// Assume that disk labels are replica of instance labels, so always change both together
			reconcileDiskLabels(ctx, computeService, instance, labels, project)
		}
	}

	return nil
}

// Only labels disks that match instance name. GKE does not allow to create multiple peristent disks per instance.
func reconcileDiskLabels(ctx context.Context, computeService compute.Service, instance compute.Instance, labels map[string]string, project string) error {

	zone := string([]rune(instance.Zone)[strings.LastIndex(instance.Zone, "/")+1:])
	resp, err := computeService.Disks.Get(project, zone, instance.Name).Context(ctx).Do()
	if err != nil {
		logging.Logger.Error(fmt.Sprintf("Error getting Disk labels for %s", instance.Name))
		return err
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
		logging.Logger.Error(fmt.Sprintf("Error setting labels Disk on %s", instance.Name))
		return err
	}

	return nil
}
