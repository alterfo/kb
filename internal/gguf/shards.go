package gguf

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type SplitInfo struct {
	Count int
	No    int
}

const maxSplitCount = 100000

func (f *File) SplitInfo() (SplitInfo, error) {
	si := SplitInfo{Count: 1, No: 0}
	if v, ok := f.MetadataValue("split.count"); ok {
		n, err := intMetadata(v, "split.count")
		if err != nil {
			return si, err
		}
		if n < 1 || n > maxSplitCount {
			return si, fmt.Errorf("gguf: split.count=%d out of range [1,%d]", n, maxSplitCount)
		}
		si.Count = n
	}
	if v, ok := f.MetadataValue("split.no"); ok {
		n, err := intMetadata(v, "split.no")
		if err != nil {
			return si, err
		}
		si.No = n
	}
	return si, nil
}

func intMetadata(v Value, key string) (int, error) {
	i, err := v.AsInt64()
	if err != nil {
		return 0, fmt.Errorf("gguf: %s: %w", key, err)
	}
	if int64(int(i)) != i {
		return 0, fmt.Errorf("gguf: %s value %d out of int range", key, i)
	}
	return int(i), nil
}

type Shard struct {
	Path    string
	ShardNo int
	Size    int64
}

func DiscoverShards(firstPath string) ([]Shard, error) {
	f, err := Open(firstPath)
	if err != nil {
		return nil, err
	}
	si, err := f.SplitInfo()
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}

	abs, err := filepath.Abs(firstPath)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(abs)
	base := filepath.Base(abs)

	if si.Count <= 1 {
		st, err := os.Stat(abs)
		if err != nil {
			return nil, err
		}
		return []Shard{{Path: abs, ShardNo: 0, Size: st.Size()}}, nil
	}

	stem := stripShardSuffix(base)
	out := make([]Shard, 0, si.Count)
	for i := 0; i < si.Count; i++ {
		cand, err := findShard(dir, stem, i, si.Count)
		if err != nil {
			return nil, err
		}
		st, err := os.Stat(cand)
		if err != nil {
			return nil, err
		}
		out = append(out, Shard{Path: cand, ShardNo: i, Size: st.Size()})
	}
	return out, nil
}

var (
	reShardOf    = regexp.MustCompile(`[-.]\d{5}-of-\d{5}\.gguf`)
	reShardDot   = regexp.MustCompile(`\.\d{5}\.gguf`)
	reGGUFSuffix = regexp.MustCompile(`\.gguf`)
)

func stripShardSuffix(name string) string {
	if m := reShardOf.FindStringIndex(name); m != nil {
		return name[:m[0]]
	}
	if m := reShardDot.FindStringIndex(name); m != nil {
		return name[:m[0]]
	}
	if m := reGGUFSuffix.FindStringIndex(name); m != nil {
		return name[:m[0]]
	}
	return name
}

func findShard(dir, stem string, idx, total int) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	prefixA := fmt.Sprintf("%s-%05d-of-%05d", stem, idx+1, total)
	prefixB := fmt.Sprintf("%s.%05d", stem, idx)
	var hits []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, prefixA) || strings.HasPrefix(name, prefixB) {
			hits = append(hits, filepath.Join(dir, name))
		}
	}
	sort.Strings(hits)
	if len(hits) == 0 {
		return "", fmt.Errorf("gguf: shard %d/%d of %s not found in %s", idx+1, total, stem, dir)
	}
	return hits[0], nil
}

type TensorLocation struct {
	Path       string
	Name       string
	ShardNo    int
	SplitCount int
	DataOffset int64
	Dims       []uint64
	RowDim     uint64
	NRows      uint64
	Type       uint32
	NBytes     int64
}

func (f *File) Tensor(name string) (TensorInfo, bool) {
	for _, t := range f.Tensors {
		if t.Name == name {
			return t, true
		}
	}
	return TensorInfo{}, false
}

func LocateTensor(shards []Shard, name string) (*TensorLocation, error) {
	if len(shards) == 0 {
		return nil, fmt.Errorf("gguf: no shards to locate tensor %q", name)
	}
	for _, sh := range shards {
		f, err := Open(sh.Path)
		if err != nil {
			continue
		}
		t, ok := f.Tensor(name)
		dataOffset := f.DataOffset
		if ok {
			nb, sizeErr := TensorSize(t.Dims, t.Type)
			f.Close()
			if sizeErr != nil {
				return nil, fmt.Errorf("gguf: tensor %q in %s: %w", name, sh.Path, sizeErr)
			}
			loc := &TensorLocation{
				Path:       sh.Path,
				Name:       t.Name,
				ShardNo:    sh.ShardNo,
				SplitCount: len(shards),
				DataOffset: dataOffset + int64(t.Offset),
				Dims:       t.Dims,
				Type:       t.Type,
				NBytes:     int64(nb),
			}
			switch {
			case len(t.Dims) == 0:
				loc.RowDim = 1
				loc.NRows = 1
			case len(t.Dims) == 1:
				loc.RowDim = t.Dims[0]
				loc.NRows = 1
			default:
				loc.RowDim = t.Dims[0]
				loc.NRows = t.Dims[1]
			}
			if loc.DataOffset < 0 || loc.NBytes < 0 || loc.DataOffset > sh.Size-loc.NBytes {
				return nil, fmt.Errorf("gguf: tensor %q data (offset %d, %d bytes) does not fit in shard %s (size %d); a tensor split across shards is unsupported", name, loc.DataOffset, loc.NBytes, sh.Path, sh.Size)
			}
			return loc, nil
		}
		f.Close()
	}
	return nil, fmt.Errorf("gguf: tensor %q not found in any shard", name)
}

func TensorSize(dims []uint64, qtype uint32) (uint64, error) {
	blockSize, blockBytes, err := quantInfo(qtype)
	if err != nil {
		return 0, err
	}
	elems := uint64(1)
	for _, d := range dims {
		elems *= d
	}
	if blockSize > 1 && elems%blockSize != 0 {
		return 0, fmt.Errorf("gguf: quant type %d has block size %d but tensor has %d elements", qtype, blockSize, elems)
	}
	return elems / blockSize * blockBytes, nil
}

func quantInfo(qtype uint32) (uint64, uint64, error) {
	switch qtype {
	case 0:
		return 1, 4, nil
	case 1:
		return 1, 2, nil
	case 2:
		return 32, 18, nil
	case 3:
		return 32, 20, nil
	case 6:
		return 32, 22, nil
	case 7:
		return 32, 24, nil
	case 8:
		return 32, 34, nil
	case 9:
		return 32, 40, nil
	case 10:
		return 256, 84, nil
	case 11:
		return 256, 110, nil
	case 12:
		return 256, 144, nil
	case 13:
		return 256, 176, nil
	case 14:
		return 256, 210, nil
	case 15:
		return 256, 292, nil
	default:
		return 0, 0, fmt.Errorf("gguf: unsupported quant type %d", qtype)
	}
}
