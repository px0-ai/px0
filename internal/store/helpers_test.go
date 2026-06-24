package store_test

import "github.com/google/uuid"

func nonExistentUUID() uuid.UUID {
	return uuid.MustParse("00000000-0000-0000-0000-000000000001")
}
