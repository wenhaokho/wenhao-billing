// Package matching scores payments against open invoices.
package matching

// Ratio is Python's difflib.SequenceMatcher(None, a, b).ratio(), including
// autojunk, computed over runes the way Python works over code points. It does
// not lower-case or strip: the engine does that before calling.
func Ratio(a, b string) float64 { //nolint:forbidigo // not money: a 0..1 similarity score
	ra, rb := []rune(a), []rune(b)
	total := len(ra) + len(rb)
	if total == 0 {
		return 1.0
	}
	m := newMatcher(ra, rb)
	return 2.0 * float64(m.matchedRunes()) / float64(total) //nolint:forbidigo // not money: a 0..1 similarity score
}

type matcher struct {
	a, b []rune
	// b2j maps each non-popular rune of b to its ascending indices in b.
	b2j map[rune][]int
}

// newMatcher is __chain_b with isjunk=None and autojunk=True: a rune of b that
// occurs more than n//100+1 times is "popular" and dropped from b2j, once b has
// 200 or more runes.
func newMatcher(a, b []rune) *matcher {
	b2j := make(map[rune][]int)
	for i, r := range b {
		b2j[r] = append(b2j[r], i)
	}
	if n := len(b); n >= 200 {
		ntest := n/100 + 1
		for r, idxs := range b2j {
			if len(idxs) > ntest {
				delete(b2j, r)
			}
		}
	}
	return &matcher{a: a, b: b, b2j: b2j}
}

// findLongestMatch is SequenceMatcher.find_longest_match. With no isjunk the
// junk-extension loops never fire, but the popular-extension ones do: a popular
// rune is not in b2j yet still extends a match that touches it.
func (m *matcher) findLongestMatch(alo, ahi, blo, bhi int) (besti, bestj, bestsize int) {
	besti, bestj = alo, blo
	j2len := map[int]int{}
	for i := alo; i < ahi; i++ {
		newj2len := map[int]int{}
		for _, j := range m.b2j[m.a[i]] {
			if j < blo {
				continue
			}
			if j >= bhi {
				break
			}
			k := j2len[j-1] + 1
			newj2len[j] = k
			if k > bestsize {
				besti, bestj, bestsize = i-k+1, j-k+1, k
			}
		}
		j2len = newj2len
	}

	for besti > alo && bestj > blo && m.a[besti-1] == m.b[bestj-1] {
		besti, bestj, bestsize = besti-1, bestj-1, bestsize+1
	}
	for besti+bestsize < ahi && bestj+bestsize < bhi && m.a[besti+bestsize] == m.b[bestj+bestsize] {
		bestsize++
	}
	return besti, bestj, bestsize
}

// matchedRunes is the sum of the sizes of get_matching_blocks. Python merges
// adjacent blocks and sorts them; neither changes the sum.
func (m *matcher) matchedRunes() int {
	type span struct{ alo, ahi, blo, bhi int }
	queue := []span{{0, len(m.a), 0, len(m.b)}}
	matched := 0
	for len(queue) > 0 {
		s := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		i, j, k := m.findLongestMatch(s.alo, s.ahi, s.blo, s.bhi)
		if k == 0 {
			continue
		}
		matched += k
		if s.alo < i && s.blo < j {
			queue = append(queue, span{s.alo, i, s.blo, j})
		}
		if i+k < s.ahi && j+k < s.bhi {
			queue = append(queue, span{i + k, s.ahi, j + k, s.bhi})
		}
	}
	return matched
}
