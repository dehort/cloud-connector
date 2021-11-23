package connection_repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/RedHatInsights/cloud-connector/internal/config"
	"github.com/RedHatInsights/cloud-connector/internal/controller"
	"github.com/RedHatInsights/cloud-connector/internal/domain"
	"github.com/RedHatInsights/cloud-connector/internal/platform/logger"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

type PermittedAccountConnectionLocator struct {
	database     *sql.DB
	proxyFactory controller.ConnectorClientProxyFactory
}

func NewPermittedAccountConnectionLocator(cfg *config.Config, database *sql.DB, proxyFactory controller.ConnectorClientProxyFactory) (*PermittedAccountConnectionLocator, error) {

	return &PermittedAccountConnectionLocator{
		database:     database,
		proxyFactory: proxyFactory,
	}, nil
}

func (pacl *PermittedAccountConnectionLocator) GetConnection(ctx context.Context, account domain.AccountID, client_id domain.ClientID) controller.ConnectorClient {
	var conn controller.ConnectorClient
	var err error

	callDurationTimer := prometheus.NewTimer(metrics.sqlLookupConnectionByAccountOrPermittedAccountAndClientIDDuration)
	defer callDurationTimer.ObserveDuration()

	// Match a connection if the account number matches either the primary account (account field) or
	// if the account number is within the permitted_accounts list
	statement, err := pacl.database.Prepare(`SELECT account, client_id, dispatchers, permitted_accounts FROM connections
        WHERE (account = $1 OR permitted_accounts @> to_jsonb($1::text))
        AND client_id = $2`)
	if err != nil {
		logger.LogError("SQL Prepare failed", err)
		return nil
	}
	defer statement.Close()

	var primaryAccount string
	var clientId string
	var dispatchersString sql.NullString
	var permittedAccounts sql.NullString
	err = statement.QueryRow(account, client_id).Scan(&primaryAccount, &clientId, &dispatchersString, &permittedAccounts)

	if err != nil {
		if err != sql.ErrNoRows {
			logger.LogError("SQL query failed:", err)
		}
		return nil
	}

	var dispatchers domain.Dispatchers
	if dispatchersString.Valid {
		err = json.Unmarshal([]byte(dispatchersString.String), &dispatchers)
		if err != nil {
			logger.LogErrorWithAccountAndClientId("Unable to unmarshal dispatchers from database", err, account, client_id)
		}
	}

	if primaryAccount != string(account) {
		logger.Log.WithFields(logrus.Fields{"client_id": client_id,
			"account":            account,
			"primary_account":    primaryAccount,
			"permitted_accounts": permittedAccounts}).Info("Connection located based on permitted account match")
	}

	conn, err = pacl.proxyFactory.CreateProxy(ctx, domain.AccountID(account), domain.ClientID(client_id), dispatchers)
	if err != nil {
		logger.LogErrorWithAccountAndClientId("Unable to create the proxy", err, account, client_id)
		return nil
	}

	return conn
}

func (pacl *PermittedAccountConnectionLocator) GetConnectionsByAccount(ctx context.Context, account domain.AccountID, offset int, limit int) (map[domain.ClientID]controller.ConnectorClient, int, error) {
	return nil, 0, errors.New("Not implemented!")
}

func (pacl *PermittedAccountConnectionLocator) GetAllConnections(ctx context.Context, offset int, limit int) (map[domain.AccountID]map[domain.ClientID]controller.ConnectorClient, int, error) {
	return nil, 0, errors.New("Not implemented!")
}
