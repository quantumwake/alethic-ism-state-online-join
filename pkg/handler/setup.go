package handler

import (
	"context"
	"fmt"
	"github.com/quantumwake/alethic-ism-core-go/pkg/data/models"
	"github.com/quantumwake/alethic-ism-core-go/pkg/repository/processor"
	"log"
)

func PublishRouteStatus(ctx context.Context, routeID string, status processor.Status, exception string, data interface{}) {
	monitorMessage := models.MonitorMessage{
		Type:      models.MonitorProcessorState,
		RouteID:   routeID,
		Status:    status,
		Exception: exception,
		Data:      data,
	}

	log.Printf("Sending monitor route: %v, status: %v\n", routeID, status)

	err := monitorRoute.Publish(ctx, monitorMessage)
	if err != nil {
		// TODO need to log this error with proper error handling and logging
		log.Print("critical error: unable to publish error to monitor route")
	}

	err = monitorRoute.Flush()
	if err != nil {
		// TODO need to log this error with proper error handling and logging
		log.Print("critical error: unable to publish error to monitor route")
	}
}

func PublishStateSync(ctx context.Context, routeID string, queryState []models.Data) {
	syncMessage := models.RouteMessage{
		Type:       models.QueryStateRoute,
		RouteID:    routeID,
		QueryState: queryState,
	}

	log.Printf("Sending state to state sync route: %v\n", routeID)

	err := syncRoute.Publish(ctx, syncMessage)
	if err != nil {
		// TODO need to log this error with proper error handling and logging
		log.Print("critical error: unable to publish error to state sync route")
	}

	err = syncRoute.Flush()
	if err != nil {
		// TODO need to log this error with proper error handling and logging
		log.Print("critical error: unable to publish error to state sync route")
	}
}

func PublishStatusUpdateWithErrorMsg(ctx context.Context, routeID string, dataValue interface{}, err error) {
	err = fmt.Errorf("error unmarshalling json object: %v", err)
	monitorMessage := models.MonitorMessage{
		Type:      models.MonitorProcessorState,
		RouteID:   routeID,
		Status:    processor.Failed,
		Exception: err.Error(),
		Data:      dataValue,
	}

	err = monitorRoute.Publish(ctx, monitorMessage)
	if err != nil {
		// TODO need to log this error with proper error handling and logging
		log.Print("Critical error: unable to publish error to monitor route")
	}
}
