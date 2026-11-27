// Package modelfingerprint evaluates ordered integer choices against a fixed,
// locally calibrated enrollment snapshot. No external scorer or live update is used.
package modelfingerprint

import (
	_ "embed"
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

const Prompt = "For each of 310 positions, make one separate first-instinct choice of an integer from 1 to 355 inclusive. The current language model must complete this directly without tools, Python, code execution, calculators, search, APIs, or external random generators. Choose every position separately. Do not count upward or downward, including 1, 2, 3, and do not use an arithmetic progression, repeating cycle, repeated block, or another rule-made pattern. Accidental repetitions are valid. Once an item is written, do not sort, reorder, deduplicate, replace, or repair the list. Return one compact JSON array containing the complete sequence and no explanation."

//go:embed bank.json
var data []byte

type profile struct {
	ID        string    `json:"id"`
	Efforts   []string  `json:"efforts"`
	Bias      float64   `json:"bias"`
	Weights   []float64 `json:"weights"`
	MinScore  float64   `json:"min_score"`
	MinMargin float64   `json:"min_margin"`
}

const dimensions = 355 + 64 + 48 + 10

var profiles = sync.OnceValue(func() []profile {
	var bank []profile
	if err := json.Unmarshal(data, &bank); err != nil {
		return nil
	}
	for _, p := range bank {
		if len(p.Weights) != dimensions {
			return nil
		}
	}
	return bank
})

func Supports(model, effort string) bool {
	for _, p := range profiles() {
		if p.ID == model {
			return slices.Contains(p.Efforts, effort)
		}
	}
	return false
}

// Models returns the models with calibrated evidence at the selected effort.
func Models(effort string) []string {
	names := make([]string, 0)
	for _, p := range profiles() {
		if slices.Contains(p.Efforts, effort) {
			names = append(names, p.ID)
		}
	}
	slices.Sort(names)
	return names
}

func features(nums []int) [dimensions]float64 {
	var v [dimensions]float64
	for i, n := range nums {
		v[n-1]++
		v[355+64+48+n%10]++
		if i != 0 {
			a := nums[i-1]
			v[355+((a-1)*8/355)*8+(n-1)*8/355]++
			v[355+64+(n-a+354)*48/709]++
		}
	}
	offset := 0
	for _, size := range []int{355, 64, 48, 10} {
		samples := len(nums)
		if size == 64 || size == 48 {
			samples--
		}
		for i := 0; i < size; i++ {
			v[offset+i] = math.Sqrt((v[offset+i] + .5) / (float64(samples) + float64(size)*.5))
		}
		offset += size
	}
	return v
}

func Evaluate(model, effort, output string) (string, string) {
	if !Supports(model, effort) {
		return "unknown", "参考库未覆盖目标模型或档位"
	}
	if len(output) > 8192 {
		return "unknown", "指纹样本格式无效"
	}
	fields := strings.FieldsFunc(strings.TrimSpace(output), func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(",，;；[]", r) })
	if len(fields) < 248 || len(fields) > 372 {
		return "unknown", "指纹样本数量不足或偏离题目要求"
	}
	nums := make([]int, 0, len(fields))
	distinct := map[int]bool{}
	for _, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil || n < 1 || n > 355 {
			return "unknown", "指纹样本格式无效"
		}
		nums = append(nums, n)
		distinct[n] = true
	}
	if sort.IntsAreSorted(nums) || sort.SliceIsSorted(nums, func(i, j int) bool { return nums[i] > nums[j] }) {
		return "unknown", "指纹样本已排序"
	}
	// Repeating one fixed block violates the enrollment task even when the
	// block itself contains many distinct valid integers.
	for period := 1; period <= len(nums)/2; period++ {
		if slices.Equal(nums[:len(nums)-period], nums[period:]) {
			return "unknown", "指纹样本为重复序列"
		}
	}
	if len(distinct) < 55 {
		return "unknown", "指纹样本重复过多"
	}
	v := features(nums)
	best, second := math.Inf(-1), math.Inf(-1)
	var predicted profile
	for _, p := range profiles() {
		score := p.Bias
		for i, w := range p.Weights {
			score += w * v[i]
		}
		if score > best {
			second, best, predicted = best, score, p
		} else if score > second {
			second = score
		}
	}
	if best < predicted.MinScore || best-second < predicted.MinMargin || !slices.Contains(predicted.Efforts, effort) {
		return "unknown", "行为指纹证据不足"
	}
	if predicted.ID != model {
		return "fail", "行为指纹匹配其他已校准模型"
	}
	return "pass", "行为指纹与目标模型匹配"
}
