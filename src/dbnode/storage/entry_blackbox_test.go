// Copyright (c) 2018 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package storage

import (
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"

	"github.com/m3db/m3/src/dbnode/storage/series"
	"github.com/m3db/m3/src/x/ident"
	xtest "github.com/m3db/m3/src/x/test"
	xtime "github.com/m3db/m3/src/x/time"
)

var (
	initTime      = time.Date(2018, time.May, 12, 15, 55, 0, 0, time.UTC)
	testBlockSize = 24 * time.Hour
)

func newTime(n int) xtime.UnixNano {
	t := initTime.Truncate(testBlockSize).Add(time.Duration(n) * testBlockSize)
	return xtime.ToUnixNano(t)
}

func newMockSeries(ctrl *gomock.Controller) series.DatabaseSeries {
	id := ident.StringID("foo")
	series := series.NewMockDatabaseSeries(ctrl)
	series.EXPECT().ID().Return(id).AnyTimes()
	return series
}

func TestEntryReaderWriterCount(t *testing.T) {
	ctrl := xtest.NewController(t)
	defer ctrl.Finish()

	e := NewEntry(NewEntryOptions{Series: newMockSeries(ctrl)})
	require.Equal(t, int32(0), e.ReaderWriterCount())

	e.IncrementReaderWriterCount()
	require.Equal(t, int32(1), e.ReaderWriterCount())

	e.DecrementReaderWriterCount()
	require.Equal(t, int32(0), e.ReaderWriterCount())
}

func TestEntryIndexSuccessPath(t *testing.T) {
	ctrl := xtest.NewController(t)
	defer ctrl.Finish()

	e := NewEntry(NewEntryOptions{Series: newMockSeries(ctrl)})
	t0 := newTime(0)
	require.False(t, e.IndexedForBlockStart(t0))

	require.True(t, e.NeedsIndexUpdate(t0))
	e.OnIndexPrepare(t0)
	e.OnIndexSuccess(t0)
	e.OnIndexFinalize(t0)

	require.True(t, e.IndexedForBlockStart(t0))
	require.Equal(t, int32(0), e.ReaderWriterCount())
	require.False(t, e.NeedsIndexUpdate(t0))
}

func TestEntryIndexFailPath(t *testing.T) {
	ctrl := xtest.NewController(t)
	defer ctrl.Finish()

	e := NewEntry(NewEntryOptions{Series: newMockSeries(ctrl)})
	t0 := newTime(0)
	require.False(t, e.IndexedForBlockStart(t0))

	require.True(t, e.NeedsIndexUpdate(t0))
	e.OnIndexPrepare(t0)
	e.OnIndexFinalize(t0)

	require.False(t, e.IndexedForBlockStart(t0))
	require.Equal(t, int32(0), e.ReaderWriterCount())
	require.True(t, e.NeedsIndexUpdate(t0))
}

func TestEntryMultipleGoroutinesRaceIndexUpdate(t *testing.T) {
	ctrl := xtest.NewController(t)
	defer ctrl.Finish()

	defer leaktest.CheckTimeout(t, time.Second)()

	e := NewEntry(NewEntryOptions{Series: newMockSeries(ctrl)})
	t0 := newTime(0)
	require.False(t, e.IndexedForBlockStart(t0))

	var (
		r1, r2 bool
		wg     sync.WaitGroup
	)
	wg.Add(2)

	go func() {
		r1 = e.NeedsIndexUpdate(t0)
		wg.Done()
	}()

	go func() {
		r2 = e.NeedsIndexUpdate(t0)
		wg.Done()
	}()

	wg.Wait()

	require.False(t, r1 && r2)
	require.True(t, r1 || r2)
}

func TestEntryTryMarkIndexGarbageCollectedAfterSeriesClose(t *testing.T) {
	ctrl := xtest.NewController(t)
	defer ctrl.Finish()

	opts := DefaultTestOptions()
	ctx := opts.ContextPool().Get()
	defer ctx.Close()

	shard := testDatabaseShard(t, opts)
	defer func() {
		require.NoError(t, shard.Close())
	}()

	id := ident.StringID("foo")

	series := series.NewMockDatabaseSeries(ctrl)
	series.EXPECT().ID().Return(id)

	entry := NewEntry(NewEntryOptions{
		Shard:  shard,
		Series: series,
	})

	// Make sure when ID is returned nil to emulate series being closed
	// and TryMarkIndexGarbageCollected calling back into shard with a nil ID.
	series.EXPECT().ID().Return(nil).AnyTimes()
	series.EXPECT().IsEmpty().Return(false).AnyTimes()
	require.NotPanics(t, func() {
		// Make sure doesn't panic.
		require.False(t, entry.TryMarkIndexGarbageCollected())
	})
}
