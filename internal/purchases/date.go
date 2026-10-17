package purchases

import (
	"encoding/json"
	"time"
)

// Date marshals as a date-only JSON string.
type Date struct{ time.Time }

func (d Date) MarshalJSON() ([]byte, error) { return json.Marshal(d.Format(dateLayout)) }
