package projection

import "math"

// ModelScores combines the seven mutually exclusive softmax probabilities in
// IDs order. Repeated frames vote jointly for a complete video format.
type ModelScores struct {
	Samples         int
	totals          [7]float64
	confidentVotes  [7]int
	confidentScores [7]float64
}

func (s *ModelScores) Add(probabilities []float32) bool {
	if len(probabilities) != len(s.totals) {
		return false
	}
	var sum float64
	var values [7]float64
	for i, p := range probabilities {
		if math.IsNaN(float64(p)) || math.IsInf(float64(p), 0) || p < 0 || p > 1 {
			return false
		}
		sum += float64(p)
		values[i] = float64(p)
	}
	if math.Abs(sum-1) > .01 {
		return false
	}
	for i, p := range values {
		s.totals[i] += p
	}
	s.Samples++
	if winner, best, confident := modelPrediction(values); confident {
		s.confidentVotes[winner]++
		s.confidentScores[winner] += best
	}
	return true
}

func (s *ModelScores) ConfidentVotes() [7]int { return s.confidentVotes }

// BinocularLayout combines the two SBS projections when their subtype is
// uncertain. This is supporting evidence only: it cannot choose EQR or fisheye.
func (s *ModelScores) BinocularLayout() (mode Mode, score float64, confident bool) {
	p := s.Probabilities()
	p[SBS180] += p[SBSFisheye]
	p[SBSFisheye] = 0
	mode, score, confident = modelPrediction(p)
	return mode, score, s.Samples >= 2 && mode.Binocular() && confident
}

// BinocularConsensus accepts two-thirds support from individually confident
// frames only when every confident frame agrees. Uncertain views may abstain;
// a conflicting confident prediction still vetoes this supplementary path.
func (s *ModelScores) BinocularConsensus() (mode Mode, score float64, support int, accepted bool) {
	mode = Flat
	total := 0
	for candidate, votes := range s.confidentVotes {
		total += votes
		if votes > support {
			mode, support = Mode(candidate), votes
		}
	}
	score = s.confidentScores[mode] / float64(max(1, support))
	return mode, score, support, mode.Binocular() && support >= 2 && support*3 >= s.Samples*2 && total == support
}

func (s *ModelScores) Probabilities() [7]float64 {
	p := s.totals
	for i := range p {
		p[i] /= float64(max(1, s.Samples))
	}
	return p
}

func (s *ModelScores) Result() (mode Mode, score float64, decisive bool) {
	mode, score, decisive = modelPrediction(s.Probabilities())
	return mode, score, s.Samples >= 2 && decisive
}

func modelPrediction(p [7]float64) (mode Mode, score float64, confident bool) {
	mode = Flat
	best, second := 0.0, 0.0
	for i, value := range p {
		if value > best {
			second, best, mode = best, value, Mode(i)
		} else {
			second = max(second, value)
		}
	}
	return mode, best, best >= .8 && best-second >= .15
}
