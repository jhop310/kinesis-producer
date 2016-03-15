package producer

import (
	"crypto/md5"
	"sync"

	k "github.com/aws/aws-sdk-go/service/kinesis"
	"github.com/golang/protobuf/proto"
)

var (
	magicNumber = []byte{0xF3, 0x89, 0x9A, 0xC2}
)

type Aggregator struct {
	sync.Mutex
	buf    []*Record
	pkeys  []string
	nbytes int
}

// Size return how many bytes stored in the aggregator.
// including partition keys.
func (a *Aggregator) Size() int {
	a.Lock()
	defer a.Unlock()
	return a.nbytes
}

// Count return how many records stored in the aggregator.
func (a *Aggregator) Count() int {
	a.Lock()
	defer a.Unlock()
	return len(a.pkeys)
}

// Put record using `data` and `partitionKey`. This method is thread-safe.
func (a *Aggregator) Put(data []byte, partitionKey string) {
	a.Lock()
	defer a.Unlock()
	a.pkeys = append(a.pkeys, partitionKey)
	keyIndex := uint64(len(a.pkeys) - 1)
	a.buf = append(a.buf, &Record{
		Data:              data,
		PartitionKeyIndex: &keyIndex,
	})
	a.nbytes += len([]byte(partitionKey)) + len(data)
}

// Drain create an aggregated `kinesis.PutRecordsRequestEntry`
// that compatible with the KCL's deaggregation logic.
//
// If you interested to know more that. see: aggregation-format.md
func (a *Aggregator) Drain() (*k.PutRecordsRequestEntry, error) {
	a.Lock()
	defer a.Unlock()
	data, err := proto.Marshal(&AggregatedRecord{
		PartitionKeyTable: a.pkeys,
		Records:           a.buf,
	})
	if err != nil {
		return nil, err
	}
	h := md5.New()
	h.Write(data)
	checkSum := h.Sum(nil)
	aggData := append(magicNumber, data...)
	aggData = append(aggData, checkSum...)
	entry := &k.PutRecordsRequestEntry{
		Data:         aggData,
		PartitionKey: &a.pkeys[0],
	}
	a.clear()
	return entry, nil
}

func (a *Aggregator) clear() {
	a.buf = make([]*Record, 0)
	a.pkeys = make([]string, 0)
	a.nbytes = 0
}

// Test if a given entry is aggregated record.
func isAggregated(entry *k.PutRecordsRequestEntry) bool {
	if len(entry.Data) < len(magicNumber) {
		return false
	}
	for i, b := range magicNumber {
		if entry.Data[i] != b {
			return false
		}
	}
	return true
}

func extractRecords(entry *k.PutRecordsRequestEntry) (out []*k.PutRecordsRequestEntry) {
	src := entry.Data[len(magicNumber) : len(entry.Data)-md5.Size]
	dest := new(AggregatedRecord)
	err := proto.Unmarshal(src, dest)
	if err != nil {
		return
	}
	for i := range dest.Records {
		r := dest.Records[i]
		out = append(out, &k.PutRecordsRequestEntry{
			Data:         r.GetData(),
			PartitionKey: &dest.PartitionKeyTable[r.GetPartitionKeyIndex()],
		})
	}
	return
}
