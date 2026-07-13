package migration

import (
	"fmt"

	"cpa-usage-keeper/internal/entities"
	"gorm.io/gorm"
)

func addUsageEventErrorDetailsMigration(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&entities.UsageEvent{}) {
		return nil
	}
	for _, field := range []string{"StatusCode", "ErrorType", "ErrorMessage"} {
		if tx.Migrator().HasColumn(&entities.UsageEvent{}, field) {
			continue
		}
		if err := tx.Migrator().AddColumn(&entities.UsageEvent{}, field); err != nil {
			return fmt.Errorf("add usage_events.%s column: %w", field, err)
		}
	}
	return nil
}
