package rtmptee

import (
	"sync/atomic"

	log "github.com/Sirupsen/logrus"
	"github.com/rcrowley/go-metrics"
)

// A fixed-size pool of fixed-size byte buffers.
type BufPool struct {

	// After receiving a buffer from the channel, you *must* call AcquireFirst().
	C <-chan *BufPoolElem

	// Buffered channel serving as a fifo queue holding all
	// available (that is, currently unused) byte buffers.
	//
	// Buffers exactly as many items as there are byte buffers.
	// This means that freed by buffers can always be returned
	// to the pool by non-blockingly sending them to the channel.
	elems chan *BufPoolElem

	nextTag int32

	avail, max int32

	availMetric metrics.Gauge
}

// A reusable fixed-size byte buffer that is a member of BufPool.
type BufPoolElem struct {

	// Pool this buffer belongs to. Doesn't change, is never nil.
	pool *BufPool

	// Slice of the backing byte array reserved for this buffer.
	// It's length is the same for all pool elements and doesn't change.
	bytes []byte

	// "Current length" of buffer.
	// This can be freely set by the buffer pool elements user, as
	// long as it stays less or equal the length of the backing slice.
	// Gets reset when the BufPoolElem is returned into the pool.
	length int

	// Reference counter.
	//
	// Will be initialized to 1 just before the BufPoolElem is
	// given out. When it reaches 0, the buffer is considered freed
	// and put back into the pool.
	refs int32

	tag int32
}

// Create a new BufPool of `nbuf` elements of `bufsize` bytes each.
func NewBufPool(nbuf, bufsize int) *BufPool {

	// We use a single backing array for all buffers for better performance.
	// We statically assign each buffer a slice of this.
	mem := make([]byte, nbuf*bufsize)

	// All currently unused buffers are held by a channel with enough buffer space.
	// We prepare the elements so we can hand them out directly via the channel.
	queue := make(chan *BufPoolElem, nbuf)
	pool := BufPool{queue, queue, int32(nbuf), int32(nbuf), int32(nbuf), metrics.NewGauge()}
	for n := 0; n < nbuf; n++ {
		queue <- &BufPoolElem{
			pool:   &pool,
			bytes:  mem[n*bufsize : (n+1)*bufsize],
			length: bufsize,
			refs:   0,
			tag:    int32(n),
		}
	}

	pool.availMetric = metrics.GetOrRegister("bufpool.avail", metrics.NewGauge()).(metrics.Gauge)
	pool.availMetric.Update(int64(nbuf))

	return &pool
}

// Returns true if all buffers are currently in the pool.
func (bp *BufPool) IsFull() bool {
	return atomic.LoadInt32(&bp.avail) == bp.max
}

// Must be called after the buffer has been received from the pool.
//
// Passing a negative value causes panic.
// Calling when the reference count has already reached 0 causes panic.
//
// Fully thread-safe.
func (elem *BufPoolElem) AcquireFirst() {
	if elem.refs != 0 {
		panic("Bug")
	}
	elem.refs = 1

	avail := atomic.AddInt32(&elem.pool.avail, -1)
	if avail < 0 {
		panic("Bug")
	}

	elem.pool.availMetric.Update(int64(avail))
}

// Increase reference count by `refs`.
//
// Passing a negative value causes panic.
// Calling when the reference count has already reached 0 causes panic.
//
// Fully thread-safe.
func (elem *BufPoolElem) Acquire(refs int32) {
	if refs < 0 {
		panic("Bug: parameter must be positive")
	}
	if atomic.AddInt32(&elem.refs, refs) <= refs {
		// Reference counter <= 0 before the AddInt32 call.
		panic("Bug: use after free")
	}
}

// Decrease reference count.
//
// When the reference count reaches 0, the buffer is considered fully freed
// and it will be returned into the pool, so it must not be used anymore
// and all references to it must be relinquished.
//
// Calling when the reference count has already reached 0 causes panic.
//
// Fully thread-safe.
func (elem *BufPoolElem) Free() {
	if elem == nil {
		panic("Bug: tried to free nil element")
	}

	remain := atomic.AddInt32(&elem.refs, -1)
	if remain == 0 {
		elem.refs = 0
		elem.length = len(elem.bytes)
		elem.tag = atomic.AddInt32(&elem.pool.nextTag, 1)

		avail := atomic.AddInt32(&elem.pool.avail, 1)
		if avail > elem.pool.max {
			panic("Bug")
		}

		select {
		case elem.pool.elems <- elem:
			// ok
		default:
			log.Panic("Bug: requeuing an element should never block but did")
		}
	} else if remain < 0 {
		log.Panic("Bug: tried to free more references than acquired")
	}
}

// Returns a slice of bytes that can be read and written to.
//
// The size of the slice can be set via SetSize().
// Initially, the size will be the maximum size, see GetMaxSize().
//
// Thread-safe if the "current size" isn't changed at the same time.
func (elem *BufPoolElem) GetBuffer() []byte {
	return elem.bytes[:elem.length]
}

// Gets the maximum size of the buffer.
//
// It is the same for all pool elements and does not change.
func (elem *BufPoolElem) GetMaxSize() int {
	return len(elem.bytes)
}

// Sets the size of the buffer.
//
// GetBuffer() will return a slice of this size.
//
// Trying to set a size larger than the maximum size (see GetMaxSize()) causes panic.
func (elem *BufPoolElem) SetSize(n int) {
	if n <= len(elem.bytes) {
		elem.length = n
	} else {
		log.Panicf("Bug: tried to set size to %d but maximum is %d", n, len(elem.bytes))
	}
}
