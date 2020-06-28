package main

import (
	autolabel "autolabel"
	"autolabel/logging"
	"context"
	"syscall"

	"google.golang.org/api/pubsub/v1"
)

func main() {
	if err := autolabel.GKEInstanceAutoLabeler(context.Background(), pubsub.PubsubMessage{}); err != nil {
		logging.Logger.Error(err.Error())
		syscall.Exit(1)
	}
}
