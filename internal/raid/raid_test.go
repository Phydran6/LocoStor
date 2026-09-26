package raid

import (
	"bufio"
	"strings"
	"testing"
)

const sample = `Personalities : [raid1] [raid6] [raid5] [raid4]
md1 : active raid5 sdd[3] sdc[1] sdb[0](F)
      1953260544 blocks super 1.2 level 5, 512k chunk, algorithm 2 [3/2] [U_U]
      [=>...................]  recovery =  8.5% (83200000/976630272) finish=90.3min speed=164922K/sec
      bitmap: 2/8 pages [8KB], 65536KB chunk

md0 : active raid1 sdf1[1] sde1[0]
      976630464 blocks super 1.2 [2/2] [UU]
      bitmap: 0/8 pages [0KB], 65536KB chunk

md2 : inactive sdg[0](S)
      976630464 blocks super 1.2

unused devices: <none>
`

func TestParse(t *testing.T) {
	arrays, err := Parse(bufio.NewScanner(strings.NewReader(sample)))
	if err != nil {
		t.Fatal(err)
	}
	if len(arrays) != 3 {
		t.Fatalf("want 3 arrays, got %d", len(arrays))
	}
	md0, md1, md2 := arrays[0], arrays[1], arrays[2]

	if md0.Level != "raid1" || md0.Status != "UU" || md0.RaidDisks != 2 || md0.ActiveDisks != 2 {
		t.Errorf("md0 parsed wrong: %+v", md0)
	}
	if md0.SizeBytes != 976630464*1024 {
		t.Errorf("md0 size = %d", md0.SizeBytes)
	}
	if h := health(&md0); h != "ok" {
		t.Errorf("md0 health = %s", h)
	}

	if md1.SyncAction != "recovery" || md1.Progress != 8.5 || md1.Finish != "90.3min" {
		t.Errorf("md1 progress parsed wrong: %+v", md1)
	}
	if md1.Members[0].Name != "sdb" || md1.Members[0].State != "faulty" {
		t.Errorf("md1 member 0 = %+v", md1.Members[0])
	}
	if m := md1.Members[2]; m.Name != "sdd" || m.State != "rebuilding" {
		t.Errorf("md1 member 2 = %+v, want sdd rebuilding", m)
	}
	if h := health(&md1); h != "rebuilding" {
		t.Errorf("md1 health = %s", h)
	}

	if md2.State != "inactive" || md2.Members[0].State != "spare" {
		t.Errorf("md2 parsed wrong: %+v", md2)
	}
	if h := health(&md2); h != "inactive" {
		t.Errorf("md2 health = %s", h)
	}
}
