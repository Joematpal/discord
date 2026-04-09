package discord

import (
	"encoding/json"
	"strconv"
)

// Snowflake is a Discord snowflake ID. It marshals as a JSON string per
// Discord's API convention, but is stored as uint64 internally.
type Snowflake uint64

func (s Snowflake) String() string { return strconv.FormatUint(uint64(s), 10) }

func (s Snowflake) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

func (s *Snowflake) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		// Try as number fallback.
		var n uint64
		if err2 := json.Unmarshal(b, &n); err2 != nil {
			return err
		}
		*s = Snowflake(n)
		return nil
	}
	if str == "" {
		*s = 0
		return nil
	}
	n, err := strconv.ParseUint(str, 10, 64)
	if err != nil {
		return err
	}
	*s = Snowflake(n)
	return nil
}

// ParseSnowflake parses a string snowflake ID.
func ParseSnowflake(s string) (Snowflake, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	return Snowflake(n), err
}
