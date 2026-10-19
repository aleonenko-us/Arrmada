package music

import "math"

// assignMin solves the assignment problem on cost (rows × cols, any shape): it returns,
// for each row, the column it is assigned to (-1 when it has none), choosing the pairing
// with the smallest total cost. The matrix is padded to a square with padCost, so with
// more rows than columns (files than tracks, or the reverse) the extras stay unassigned,
// and the ones left over are those whose pairings would cost the most. Callers still
// judge each pair on its own (the matcher's track maximum).
//
// This is the Hungarian (Munkres) algorithm in its O(n³) form with row and column
// potentials (Kuhn's method as Jonker and Volgenant tightened it). A box set of a few
// hundred tracks is well within reach.
func assignMin(cost [][]float64, padCost float64) []int {
	rows := len(cost)
	cols := 0
	for _, r := range cost {
		if len(r) > cols {
			cols = len(r)
		}
	}
	out := make([]int, rows)
	for i := range out {
		out[i] = -1
	}
	if rows == 0 || cols == 0 {
		return out
	}
	n := rows
	if cols > n {
		n = cols
	}
	// a is 1-based, as the algorithm is usually written: a[i][j] for i, j in 1..n.
	at := func(i, j int) float64 {
		if i <= rows && j <= len(cost[i-1]) {
			if c := cost[i-1][j-1]; !math.IsNaN(c) {
				return c
			}
		}
		return padCost
	}
	u := make([]float64, n+1)
	v := make([]float64, n+1)
	p := make([]int, n+1) // p[j]: the row assigned to column j (0 = none)
	way := make([]int, n+1)
	for i := 1; i <= n; i++ {
		p[0] = i
		j0 := 0
		minv := make([]float64, n+1)
		used := make([]bool, n+1)
		for j := range minv {
			minv[j] = math.Inf(1)
		}
		for {
			used[j0] = true
			i0 := p[j0]
			delta := math.Inf(1)
			j1 := 0
			for j := 1; j <= n; j++ {
				if used[j] {
					continue
				}
				cur := at(i0, j) - u[i0] - v[j]
				if cur < minv[j] {
					minv[j], way[j] = cur, j0
				}
				if minv[j] < delta {
					delta, j1 = minv[j], j
				}
			}
			for j := 0; j <= n; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
			if j0 == 0 {
				break
			}
		}
	}
	for j := 1; j <= n; j++ {
		i := p[j]
		if i >= 1 && i <= rows && j <= len(cost[i-1]) {
			out[i-1] = j - 1
		}
	}
	return out
}
