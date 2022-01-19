// +build sql

package connection_repository

import (
	"context"
	"testing"

	"github.com/RedHatInsights/cloud-connector/internal/config"
	"github.com/RedHatInsights/cloud-connector/internal/controller"
	"github.com/RedHatInsights/cloud-connector/internal/domain"
	"github.com/RedHatInsights/cloud-connector/internal/platform/db"
	"github.com/RedHatInsights/cloud-connector/internal/platform/logger"

	"database/sql"
)

func init() {
	logger.InitLogger()
}

func insertTestData(database *sql.DB, account domain.AccountID, client_id domain.ClientID, permitted_tenants string) (func(), error) {
	insert := "INSERT INTO connections (account, client_id, permitted_tenants) VALUES ($1, $2, $3)"
	delete := "DELETE FROM connections WHERE account = $1 AND client_id = $2"

	noOpFunc := func() {}

	statement, err := database.Prepare(insert)
	if err != nil {
		return noOpFunc, err
	}
	defer statement.Close()

	_, err = statement.Exec(account, client_id, permitted_tenants)
	if err != nil {
		return noOpFunc, err
	}

	cleanUpTestDataFunc := func() {
		statement, err := database.Prepare(delete)
		if err != nil {
			return
		}
		defer statement.Close()

		_, err = statement.Exec(account, client_id)
		if err != nil {
			return
		}
	}

	return cleanUpTestDataFunc, nil
}

type mockConnectorClientProxyFactory struct {
	callCount int
	account   domain.AccountID
	clientID  domain.ClientID
}

func (m *mockConnectorClientProxyFactory) CreateProxy(ctx context.Context, account domain.AccountID, clientID domain.ClientID, dispatchers domain.Dispatchers) (controller.ConnectorClient, error) {
	m.callCount += 1
	m.account = account
	m.clientID = clientID
	return nil, nil
}

func TestPermittedTenantConnectionLocator(t *testing.T) {

	cfg := config.GetConfig()

	database, err := db.InitializeDatabaseConnection(cfg)
	if err != nil {
		t.Fatal("Unable to connect to database: ", err)
	}

	testCases := []struct {
		testName            string
		account             domain.AccountID
		clientID            domain.ClientID
		permittedTenants    string
		accountToSearchFor  domain.AccountID
		clientIDToSearchFor domain.ClientID
		verifyResults       func(*testing.T, mockConnectorClientProxyFactory)
	}{
		{"primary account match", "999999", "client-1", "[]", "999999", "client-1", verifyProxyFactoryWasCalled("999999", "client-1")},
		{"permitted tenant match", "888888", "client-2", "[\"0001\", \"0002\"]", "0001", "client-2", verifyProxyFactoryWasCalled("0001", "client-2")},
		{"no account matches", "999999", "client-3", "[]", "888888", "client-3", verifyProxyFactoryWasNotCalled()},
		{"account matches, but client-id does not", "999999", "client-4", "[]", "999999", "will-not-find-this-client", verifyProxyFactoryWasNotCalled()},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {

			var mockProxyFactory = mockConnectorClientProxyFactory{}

			var connectionLocator ConnectionLocator
			connectionLocator, err := NewPermittedTenantConnectionLocator(cfg, database, &mockProxyFactory)
			if err != nil {
				t.Fatal("unexpected error while creating the PermittedTenantConnectionLocator", err)
			}

			cleanUpTestData, err := insertTestData(database, tc.account, tc.clientID, tc.permittedTenants)
			if err != nil {
				t.Fatal("unexpected error while inserting test data into the database", err)
			}

			defer cleanUpTestData()

			connectionLocator.GetConnection(context.TODO(), domain.AccountID(tc.accountToSearchFor), domain.ClientID(tc.clientIDToSearchFor))

			// Use the calls to the mock proxy factory to verify the behavior is correct
			tc.verifyResults(t, mockProxyFactory)
		})
	}
}

func verifyProxyFactoryWasCalled(expectedAccount domain.AccountID, expectedClientID domain.ClientID) func(t *testing.T, mockProxyFactory mockConnectorClientProxyFactory) {
	return func(t *testing.T, mockProxyFactory mockConnectorClientProxyFactory) {
		if mockProxyFactory.callCount != 1 {
			t.Fatalf("expected proxy factory call count to be 1, but got %d!", mockProxyFactory.callCount)
		}

		if mockProxyFactory.account != expectedAccount {
			t.Fatalf("expected proxy factory to be called with account %s, but got %s!", mockProxyFactory.account, expectedAccount)
		}

		if mockProxyFactory.clientID != expectedClientID {
			t.Fatalf("expected proxy factory to be called with clientID %s, but got %s!", mockProxyFactory.clientID, expectedClientID)
		}
	}
}

func verifyProxyFactoryWasNotCalled() func(t *testing.T, mockProxyFactory mockConnectorClientProxyFactory) {
	return func(t *testing.T, mockProxyFactory mockConnectorClientProxyFactory) {
		if mockProxyFactory.callCount != 0 {
			t.Fatalf("expected proxy factory call count to be 0, but got %d!", mockProxyFactory.callCount)
		}
	}
}
