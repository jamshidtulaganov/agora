package handler

import (
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestSharedTelegramLoginStoreEnabledInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("AGORA_TELEGRAM_SHARED_LOGIN_STORE", "")

	h := &Handler{Queries: &db.Queries{}}
	if !h.sharedTelegramLoginStoreEnabled() {
		t.Fatal("production must use the shared Telegram login store")
	}
}

func TestSharedTelegramLoginStoreCanBeEnabledOutsideProduction(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("AGORA_TELEGRAM_SHARED_LOGIN_STORE", "true")

	h := &Handler{Queries: &db.Queries{}}
	if !h.sharedTelegramLoginStoreEnabled() {
		t.Fatal("explicit shared-store flag must enable the database store")
	}
}

func TestSharedTelegramLoginStoreRequiresQueries(t *testing.T) {
	t.Setenv("APP_ENV", "production")

	h := &Handler{}
	if h.sharedTelegramLoginStoreEnabled() {
		t.Fatal("shared store cannot be enabled without database queries")
	}
}
