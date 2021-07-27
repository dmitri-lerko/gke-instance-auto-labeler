# GKE Instance Auto Labeler

Simple Cloud Function that sets desired GCE Instance and Disk labels based on name match. Born as workaround to GKE limitation where it does not support setting GCE labels on the NodePools. Meaning that billing has a cluster level granularity, but not have a Node Pool level granularity. 

Implemented in Go + [Cloud Functions](https://cloud.google.com/functions/) + [PubSub](https://cloud.google.com/pubsub) + [Cloud Scheduler](https://cloud.google.com/scheduler)

## Why does this exists?

Cost management in large projects running GKE can be a challenge. While, there is a [Metered Usage](https://cloud.google.com/kubernetes-engine/docs/how-to/cluster-usage-metering) feature, it is too far removed from the rest of the billing. Instead, in plenty of the cases, it is possible to allocate cost to a team/product/division based on the instance name which is derived from a node pool. Unfortunately, GKE provides no such feature to label GCE instances serving as GKE nodes with labels. This Cloud Function solves this short-comming in a brute force manner, by re-evaluating labels on all the instances in the selected projects and adjusting them to be in line with the desired labels.

## How to use this?

Specify projects, labels to match and labels to set in `configuration.yaml`

Example:
```yaml
projects:
  - gcp-project-1
  - gcp-project-2
labels:
  search:
    team: search
    purpose: hosts-search-applications
  system:
    team: devops
    purpose: logging-and-monitoring
  transcoding:
    team: digital
    purpose: transcode-videos
    preemptible: "true"
```
If you have an instance with the name gke-prod-transcode-videos-7ebf06e4-jds5, it will match `transcode` and set relevant labels.
`Map map[team:digital purpose:transcode-videos preemptible:true] is not a subset map[goog-gke-node:]`

## Proposed configuration

Cloud Scheduler trigger PubSub Event every minute -> GKE Instance Auto Labeler Consumers PubSub Event > Cloud Function reconciles labels


## Deploying

```shell
# Prepare environment
go mod init github.com/dmitri-lerko/gke-instance-auto-labeler
go mod tidy
go build

# Create topic
gcloud pubsub topics create --labels=team=devops \ 
  reconcile-labels-every-minute --project=<INSERT PROJECT NAME>

# Create service account
gcloud iam service-accounts create gke-instance-labels

# Get Organization ID
gcloud organizations list

# Create Organization level Custom Role with the least-privilege
gcloud iam roles create GKEInstanceLabelSetter --organization=<organization-id> \
  --file=custom-role.yaml

# Bind Custom Role to the Service Account
gcloud organizations add-iam-policy-binding <organization-id> \
  --member=serviceAccount:gke-instance-labels@<INSERT PROJECT NAME>.iam.gserviceaccount.com \
  --role organizations/<organization-id>/roles/GKEInstanceLabelSetter

# Manually deploy
gcloud beta functions deploy GKEInstanceLabels --region <INSERT REGION> \
 --runtime go111 --trigger-topic=reconcile-labels-every-minute \
 --allow-unauthenticated --memory=128 --update-labels="team=devops" \
 --service-account=gke-instance-labels@<INSERT PROJECT NAME>.iam.gserviceaccount.com \
 --project=<INSERT PROJECT NAME> --timeout=540s

# Add hourly schedule
gcloud scheduler jobs create pubsub reconcile-labels-every-minute \
 --schedule "* * * * *" --topic reconcile-labels-every-minute \
 --message-body "Reconcile GKE Instance Labels every minute" \
 --project <INSERT PROJECT NAME>
```

### Warning
* Function adds new labels where missing and overrides where present, but different
* Function also updates labels on the disk attached to the instance (disk must have the same name as the instance)
* Function does nothing when existing labels match
* GKE has it's own Label reconcillation loop, therefore it is advised to not try and control labels set on the cluster level with this tool, you will see frequent label updates
* The code is not defensive, but instead, it simply returns error where it can't handle edge cases
* This function will match non-GKE instances too as it currently does not filter by GKE labels


### Permissions
This function needs following GCP permissions to operate:

* compute.disks.get
* compute.disks.setLabels
* compute.instances.list
* compute.instances.setLabels
* compute.zones.list

### Run locally

You can run the application locally by invoking:

`go run main/main.go`
