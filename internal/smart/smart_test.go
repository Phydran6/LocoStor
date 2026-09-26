package smart

import (
	"encoding/json"
	"testing"
)

const sampleSAT = `{
  "smartctl": {"exit_status": 0, "messages": []},
  "device": {"name": "/dev/sdb", "type": "sat", "protocol": "ATA"},
  "model_name": "WDC WD40EFRX",
  "serial_number": "TEST123",
  "user_capacity": {"bytes": 4000787030016},
  "rotation_rate": 5400,
  "smart_status": {"passed": true},
  "temperature": {"current": 34},
  "power_on_time": {"hours": 12345},
  "power_cycle_count": 42,
  "ata_smart_attributes": {"table": [
    {"id": 5, "name": "Reallocated_Sector_Ct", "value": 200, "worst": 200, "thresh": 140, "when_failed": "", "raw": {"value": 3, "string": "3"}},
    {"id": 194, "name": "Temperature_Celsius", "value": 116, "worst": 104, "thresh": 0, "when_failed": "", "raw": {"value": 34, "string": "34"}}
  ]}
}`

func TestConvert(t *testing.T) {
	var raw rawOutput
	if err := json.Unmarshal([]byte(sampleSAT), &raw); err != nil {
		t.Fatal(err)
	}
	d := Convert("/dev/sdb", "sat", &raw)
	if d.Model != "WDC WD40EFRX" || *d.Temperature != 34 || *d.PowerOnHours != 12345 {
		t.Errorf("unexpected disk: %+v", d)
	}
	if d.Reallocated == nil || *d.Reallocated != 3 {
		t.Errorf("reallocated not parsed")
	}
	if d.Health != "warning" {
		t.Errorf("health = %s, want warning", d.Health)
	}
}

func TestConvertStandby(t *testing.T) {
	var raw rawOutput
	_ = json.Unmarshal([]byte(`{"smartctl":{"exit_status":2,"messages":[{"string":"Device is in STANDBY mode, exit(2)","severity":"information"}]}}`), &raw)
	if d := Convert("/dev/sdc", "", &raw); d.Health != "standby" {
		t.Errorf("health = %s, want standby", d.Health)
	}
}
