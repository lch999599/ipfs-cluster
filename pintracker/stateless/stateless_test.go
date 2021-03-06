package stateless

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	rpc "github.com/hsanjuan/go-libp2p-gorpc"
	cid "github.com/ipfs/go-cid"

	"github.com/ipfs/ipfs-cluster/api"
	"github.com/ipfs/ipfs-cluster/test"
)

var (
	pinCancelCid      = test.TestCid3
	unpinCancelCid    = test.TestCid2
	ErrPinCancelCid   = errors.New("should not have received rpc.IPFSPin operation")
	ErrUnpinCancelCid = errors.New("should not have received rpc.IPFSUnpin operation")
	pinOpts           = api.PinOptions{
		ReplicationFactorMax: -1,
		ReplicationFactorMin: -1,
	}
)

type mockService struct {
	rpcClient *rpc.Client
}

func mockRPCClient(t *testing.T) *rpc.Client {
	s := rpc.NewServer(nil, "mock")
	c := rpc.NewClientWithServer(nil, "mock", s)
	err := s.RegisterName("Cluster", &mockService{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (mock *mockService) IPFSPin(ctx context.Context, in api.PinSerial, out *struct{}) error {
	c := in.ToPin().Cid
	switch c.String() {
	case test.TestSlowCid1:
		time.Sleep(2 * time.Second)
	case pinCancelCid:
		return ErrPinCancelCid
	}
	return nil
}

func (mock *mockService) IPFSUnpin(ctx context.Context, in api.PinSerial, out *struct{}) error {
	c := in.ToPin().Cid
	switch c.String() {
	case test.TestSlowCid1:
		time.Sleep(2 * time.Second)
	case unpinCancelCid:
		return ErrUnpinCancelCid
	}
	return nil
}

func (mock *mockService) IPFSPinLs(ctx context.Context, in string, out *map[string]api.IPFSPinStatus) error {
	m := map[string]api.IPFSPinStatus{
		test.TestCid1: api.IPFSPinStatusRecursive,
	}
	*out = m
	return nil
}

func (mock *mockService) IPFSPinLsCid(ctx context.Context, in api.PinSerial, out *api.IPFSPinStatus) error {
	switch in.Cid {
	case test.TestCid1, test.TestCid2:
		*out = api.IPFSPinStatusRecursive
	default:
		*out = api.IPFSPinStatusUnpinned
	}
	return nil
}

func (mock *mockService) Pins(ctx context.Context, in struct{}, out *[]api.PinSerial) error {
	*out = []api.PinSerial{
		api.PinWithOpts(test.MustDecodeCid(test.TestCid1), pinOpts).ToSerial(),
		api.PinWithOpts(test.MustDecodeCid(test.TestCid3), pinOpts).ToSerial(),
	}
	return nil
}

func (mock *mockService) PinGet(ctx context.Context, in api.PinSerial, out *api.PinSerial) error {
	switch in.Cid {
	case test.ErrorCid:
		return errors.New("expected error when using ErrorCid")
	case test.TestCid1, test.TestCid2:
		*out = api.PinWithOpts(test.MustDecodeCid(in.Cid), pinOpts).ToSerial()
		return nil
	default:
		return errors.New("not found")
	}
}

func testSlowStatelessPinTracker(t *testing.T) *Tracker {
	cfg := &Config{}
	cfg.Default()
	mpt := New(cfg, test.TestPeerID1)
	mpt.SetClient(mockRPCClient(t))
	return mpt
}

func testStatelessPinTracker(t testing.TB) *Tracker {
	cfg := &Config{}
	cfg.Default()
	spt := New(cfg, test.TestPeerID1)
	spt.SetClient(test.NewMockRPCClient(t))
	return spt
}

func TestStatelessPinTracker_New(t *testing.T) {
	spt := testStatelessPinTracker(t)
	defer spt.Shutdown()
}

func TestStatelessPinTracker_Shutdown(t *testing.T) {
	spt := testStatelessPinTracker(t)
	err := spt.Shutdown()
	if err != nil {
		t.Fatal(err)
	}
	err = spt.Shutdown()
	if err != nil {
		t.Fatal(err)
	}
}

func TestUntrackTrack(t *testing.T) {
	spt := testStatelessPinTracker(t)
	defer spt.Shutdown()

	h1 := test.MustDecodeCid(test.TestCid1)

	// LocalPin
	c := api.PinWithOpts(h1, pinOpts)

	err := spt.Track(c)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(time.Second / 2)

	err = spt.Untrack(h1)
	if err != nil {
		t.Fatal(err)
	}
}

func TestTrackUntrackWithCancel(t *testing.T) {
	spt := testSlowStatelessPinTracker(t)
	defer spt.Shutdown()

	slowPinCid := test.MustDecodeCid(test.TestSlowCid1)

	// LocalPin
	slowPin := api.PinWithOpts(slowPinCid, pinOpts)

	err := spt.Track(slowPin)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond) // let pinning start

	pInfo := spt.optracker.Get(slowPin.Cid)
	if pInfo.Status == api.TrackerStatusUnpinned {
		t.Fatal("slowPin should be tracked")
	}

	if pInfo.Status == api.TrackerStatusPinning {
		go func() {
			err = spt.Untrack(slowPinCid)
			if err != nil {
				t.Fatal(err)
			}
		}()
		select {
		case <-spt.optracker.OpContext(slowPinCid).Done():
			return
		case <-time.Tick(100 * time.Millisecond):
			t.Errorf("operation context should have been cancelled by now")
		}
	} else {
		t.Error("slowPin should be pinning and is:", pInfo.Status)
	}
}

func TestTrackUntrackWithNoCancel(t *testing.T) {
	spt := testSlowStatelessPinTracker(t)
	defer spt.Shutdown()

	slowPinCid := test.MustDecodeCid(test.TestSlowCid1)
	fastPinCid := test.MustDecodeCid(pinCancelCid)

	// SlowLocalPin
	slowPin := api.PinWithOpts(slowPinCid, pinOpts)

	// LocalPin
	fastPin := api.PinWithOpts(fastPinCid, pinOpts)

	err := spt.Track(slowPin)
	if err != nil {
		t.Fatal(err)
	}

	// Otherwise fails when running with -race
	time.Sleep(300 * time.Millisecond)

	err = spt.Track(fastPin)
	if err != nil {
		t.Fatal(err)
	}

	// fastPin should be queued because slow pin is pinning
	fastPInfo := spt.optracker.Get(fastPin.Cid)
	if fastPInfo.Status == api.TrackerStatusUnpinned {
		t.Fatal("fastPin should be tracked")
	}
	if fastPInfo.Status == api.TrackerStatusPinQueued {
		err = spt.Untrack(fastPinCid)
		if err != nil {
			t.Fatal(err)
		}
		// pi := spt.get(fastPinCid)
		// if pi.Error == ErrPinCancelCid.Error() {
		// 	t.Fatal(ErrPinCancelCid)
		// }
	} else {
		t.Error("fastPin should be queued to pin")
	}

	pi := spt.optracker.Get(fastPin.Cid)
	if pi.Cid == nil {
		t.Error("fastPin should have been removed from tracker")
	}
}

func TestUntrackTrackWithCancel(t *testing.T) {
	spt := testSlowStatelessPinTracker(t)
	defer spt.Shutdown()

	slowPinCid := test.MustDecodeCid(test.TestSlowCid1)

	// LocalPin
	slowPin := api.PinWithOpts(slowPinCid, pinOpts)

	err := spt.Track(slowPin)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(time.Second / 2)

	// Untrack should cancel the ongoing request
	// and unpin right away
	err = spt.Untrack(slowPinCid)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	pi := spt.optracker.Get(slowPin.Cid)
	if pi.Cid == nil {
		t.Fatal("expected slowPin to be tracked")
	}

	if pi.Status == api.TrackerStatusUnpinning {
		go func() {
			err = spt.Track(slowPin)
			if err != nil {
				t.Fatal(err)
			}
		}()
		select {
		case <-spt.optracker.OpContext(slowPinCid).Done():
			return
		case <-time.Tick(100 * time.Millisecond):
			t.Errorf("operation context should have been cancelled by now")
		}
	} else {
		t.Error("slowPin should be in unpinning")
	}

}

func TestUntrackTrackWithNoCancel(t *testing.T) {
	spt := testStatelessPinTracker(t)
	defer spt.Shutdown()

	slowPinCid := test.MustDecodeCid(test.TestSlowCid1)
	fastPinCid := test.MustDecodeCid(unpinCancelCid)

	// SlowLocalPin
	slowPin := api.PinWithOpts(slowPinCid, pinOpts)

	// LocalPin
	fastPin := api.PinWithOpts(fastPinCid, pinOpts)

	err := spt.Track(slowPin)
	if err != nil {
		t.Fatal(err)
	}

	err = spt.Track(fastPin)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(3 * time.Second)

	err = spt.Untrack(slowPin.Cid)
	if err != nil {
		t.Fatal(err)
	}

	err = spt.Untrack(fastPin.Cid)
	if err != nil {
		t.Fatal(err)
	}

	pi := spt.optracker.Get(fastPin.Cid)
	if pi.Cid == nil {
		t.Fatal("c untrack operation should be tracked")
	}

	if pi.Status == api.TrackerStatusUnpinQueued {
		err = spt.Track(fastPin)
		if err != nil {
			t.Fatal(err)
		}

		// pi := spt.get(fastPinCid)
		// if pi.Error == ErrUnpinCancelCid.Error() {
		// 	t.Fatal(ErrUnpinCancelCid)
		// }
	} else {
		t.Error("c should be queued to unpin")
	}
}

var sortPinInfoByCid = func(p []api.PinInfo) {
	sort.Slice(p, func(i, j int) bool {
		return p[i].Cid.String() < p[j].Cid.String()
	})
}

func TestStatelessTracker_SyncAll(t *testing.T) {
	type args struct {
		cs      []*cid.Cid
		tracker *Tracker
	}
	tests := []struct {
		name    string
		args    args
		want    []api.PinInfo
		wantErr bool
	}{
		{
			"basic stateless syncall",
			args{
				[]*cid.Cid{
					test.MustDecodeCid(test.TestCid1),
					test.MustDecodeCid(test.TestCid2),
				},
				testStatelessPinTracker(t),
			},
			[]api.PinInfo{
				api.PinInfo{
					Cid:    test.MustDecodeCid(test.TestCid1),
					Status: api.TrackerStatusPinned,
				},
				api.PinInfo{
					Cid:    test.MustDecodeCid(test.TestCid2),
					Status: api.TrackerStatusPinned,
				},
			},
			false,
		},
		{
			"slow stateless syncall",
			args{
				[]*cid.Cid{
					test.MustDecodeCid(test.TestCid1),
					test.MustDecodeCid(test.TestCid2),
				},
				testSlowStatelessPinTracker(t),
			},
			[]api.PinInfo{
				api.PinInfo{
					Cid:    test.MustDecodeCid(test.TestCid1),
					Status: api.TrackerStatusPinned,
				},
				api.PinInfo{
					Cid:    test.MustDecodeCid(test.TestCid2),
					Status: api.TrackerStatusPinned,
				},
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.args.tracker.SyncAll()
			if (err != nil) != tt.wantErr {
				t.Errorf("PinTracker.SyncAll() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if len(got) != 0 {
				t.Fatalf("should not have synced anything when it tracks nothing")
			}

			for _, c := range tt.args.cs {
				err := tt.args.tracker.Track(api.PinWithOpts(c, pinOpts))
				if err != nil {
					t.Fatal(err)
				}
				tt.args.tracker.optracker.SetError(c, errors.New("test error"))
			}

			got, err = tt.args.tracker.SyncAll()
			if (err != nil) != tt.wantErr {
				t.Errorf("PinTracker.SyncAll() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			sortPinInfoByCid(got)
			sortPinInfoByCid(tt.want)

			for i := range got {
				if got[i].Cid.String() != tt.want[i].Cid.String() {
					t.Errorf("got: %v\n want %v", got[i].Cid.String(), tt.want[i].Cid.String())
				}

				if got[i].Status != tt.want[i].Status {
					t.Errorf("got: %v\n want %v", got[i].Status, tt.want[i].Status)
				}
			}
		})
	}
}

func BenchmarkTracker_localStatus(b *testing.B) {
	tracker := testStatelessPinTracker(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.localStatus(true)
	}
}
