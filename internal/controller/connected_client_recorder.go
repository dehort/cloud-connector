package controller

import (
	"context"

	"github.com/RedHatInsights/cloud-connector/internal/domain"
	"github.com/RedHatInsights/cloud-connector/internal/platform/logger"

	"github.com/sirupsen/logrus"
)

type ConnectedClientRecorder interface {
	RecordConnectedClient(context.Context, domain.AccountID, domain.ClientID, interface{}) error
}

type InventoryBasedConnectedClientRecorder struct {
}

func (ibccr *InventoryBasedConnectedClientRecorder) RecordConnectedClient(ctx context.Context, account domain.AccountID, clientID domain.ClientID, canonicalFacts interface{}) error {
	logger := logger.Log.WithFields(logrus.Fields{"account": account, "client_id": clientID})
	logger.Debug("send inventory kafka message - ", account, clientID, canonicalFacts)
	return nil
}
