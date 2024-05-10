package cloud_connector

import (
	"github.com/google/uuid"
)

func sanitizeCanonicalFacts(cf interface{}) interface{} {

	canonicalFacts := cf.(map[string]interface{})
	sanitizedCanonicalFacts := make(map[string]interface{})

	for key, value := range canonicalFacts {
		if key == "insights_id" {
			if isValidUUID(value.(string)) {
				sanitizedCanonicalFacts[key] = value
			}
		} else {
			sanitizedCanonicalFacts[key] = value
		}
	}

	return sanitizedCanonicalFacts
}

func isValidUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}
