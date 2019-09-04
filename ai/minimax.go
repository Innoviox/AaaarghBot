package ai

import (
	//"fmt"
	"bytes"
	"log"
	"math"
	"math/rand"
	"sync/atomic"
	"time"

	"golang.org/x/net/context"

	"../bitboard"
	"../ptn"
	"../tak"
)

const (
	MaxEval      int64 = 1 << 30
	MinEval            = -MaxEval
	WinThreshold       = 1 << 29
	Illegal            = -1 << 31

	tableSize uint64 = (1 << 20)

	maxDepth = 15
)

type EvaluationFunc func(m *MinimaxAI, p *tak.Position) int64

type MinimaxAI struct {
	cfg  MinimaxConfig
	rand *rand.Rand

	st Stats
	c  bitboard.Constants

	history  map[uint64]int
	response map[uint64]tak.Move

	evaluate EvaluationFunc

	table []tableEntry
	stack [maxDepth]struct {
		p     *tak.Position
		mg    moveGenerator
		moves [500]tak.Move
		pv    [maxDepth]tak.Move
		m     tak.Move
	}

	cancel *int32
	
	Diversify int64
}

type tableEntry struct {
	hash  uint64
	depth int
	value int64
	bound boundType
	m     tak.Move
	p     *tak.Position
}

type boundType byte

const (
	lowerBound = iota
	exactBound = iota
	upperBound = iota
)

type Stats struct {
	Depth     int
	Generated uint64
	Evaluated uint64
	Scout     uint64
	Terminal  uint64
	Visited   uint64

	CutNodes   uint64
	NullSearch uint64
	NullCut    uint64
	Cut0       uint64
	Cut1       uint64
	CutSearch  uint64

	ReSearch uint64

	AllNodes uint64

	TTHits     uint64
	TTShortcut uint64
}

type MinimaxConfig struct {
	Size  int
	Depth int
	Debug int
	Seed  int64

	NoSort     bool
	NoTable    bool
	NoNullMove bool

	Evaluate EvaluationFunc
}

func NewMinimax(cfg MinimaxConfig) *MinimaxAI {
	m := &MinimaxAI{cfg: cfg}
	if m.cfg.Depth == 0 {
		m.cfg.Depth = maxDepth
	}
	m.precompute()
	m.evaluate = cfg.Evaluate
	if m.evaluate == nil {
		m.evaluate = MakeEvaluator(cfg.Size, nil)
	}
	m.history = make(map[uint64]int, m.cfg.Size*m.cfg.Size*m.cfg.Size)
	m.response = make(map[uint64]tak.Move, m.cfg.Size*m.cfg.Size*m.cfg.Size)
	if !cfg.NoTable {
		m.table = make([]tableEntry, tableSize)
	}
	for i := range m.stack {
		m.stack[i].p = tak.Alloc(m.cfg.Size)
	}

	var seed = m.cfg.Seed
	if seed == 0 {
		seed = time.Now().UnixNano() + rand.Int63()
	}
	m.rand = rand.New(rand.NewSource(seed))

	return m
}

const hashMul = 0x61C8864680B583EB

func (m *MinimaxAI) ttGet(h uint64) *tableEntry {
	if m.cfg.NoTable {
		return nil
	}
	i1 := h % tableSize
	i2 := (h * hashMul) % tableSize
	te := &m.table[i1]
	if te.hash == h {
		return te
	}
	te = &m.table[i2]
	if te.hash == h {
		return te
	}
	return nil
}

func (m *MinimaxAI) ttPut(h uint64) *tableEntry {
	if m.cfg.NoTable {
		return nil
	}
	if atomic.LoadInt32(m.cancel) != 0 {
		return nil
	}
	i1 := h % tableSize
	i2 := (h * hashMul) % tableSize
	if m.table[i1].hash != 0 {
		m.table[i2] = m.table[i1]
	}
	return &m.table[i1]
}

func (m *MinimaxAI) precompute() {
	s := uint(m.cfg.Size)
	m.c = bitboard.Precompute(s)
}

func formatpv(ms []tak.Move) string {
	var out bytes.Buffer
	out.WriteString("[")
	for i, m := range ms {
		if i != 0 {
			out.WriteString(" ")
		}
		out.WriteString(ptn.FormatMove(&m))
	}
	out.WriteString("]")
	return out.String()
}

func getThreats(p *tak.Position) ([]tak.Move){
	var threatpos *tak.Position = p.Clone()
	size2 := p.Size() * p.Size()
	var filtermask uint64
	if p.ToMove() == tak.White {
		filtermask = p.White
		threatpos.White = 0
	} else {
		filtermask = p.Black
		threatpos.Black = 0
	}
	threatpos.Caps &^= filtermask
	threatpos.Standing &^= filtermask
	for spot := 0; spot < size2; spot++ {
		var spotmask uint64 = 1 << uint(spot)
		if spotmask&filtermask != 0 {
			threatpos.Height[spot] = 0
			threatpos.Stacks[spot] = 0
		}
	}
	threatpos.IncrementMove()
	threatmoves := []tak.Move{}
	allmoves := []tak.Move{}
	allmoves = threatpos.AllMoves(allmoves)
	var searchpos *tak.Position
	var err error
	for _, move := range allmoves {
		if move.Type >= tak.SlideLeft {
			//searchpos = threatpos.Clone()
			searchpos, err = threatpos.MovePreallocated(&move, searchpos)
			if err == nil {
				over, winner := searchpos.GameOver()
				if over && threatpos.ToMove() == winner {
					threatmoves = append(threatmoves, move)
				}
			}
		}
	}
	return threatmoves
}

func (m *MinimaxAI) GetMove1(ctx context.Context, p *tak.Position, spread float64) (tak.Move, float64) {
	if m.cfg.Size != p.Size() {
		panic("Analyze: wrong size")
	}
	var threatpos *tak.Position = p.Clone()
	size2 := p.Size() * p.Size()
	var filtermask uint64
	if p.ToMove() == tak.White {
		filtermask = p.White
		threatpos.White = 0
	} else {
		filtermask = p.Black
		threatpos.Black = 0
	}
	threatpos.Caps &^= filtermask
	threatpos.Standing &^= filtermask
	for spot := 0; spot < size2; spot++ {
		var spotmask uint64 = 1 << uint(spot)
		if spotmask&filtermask != 0 {
			threatpos.Height[spot] = 0
			threatpos.Stacks[spot] = 0
		}
	}
	threatpos.IncrementMove()
	threatmoves := []tak.Move{}
	allmoves := []tak.Move{}
	allmoves = threatpos.AllMoves(allmoves)
	var searchpos *tak.Position
	var err error
	for _, move := range allmoves {
		if move.Type >= tak.SlideLeft {
			//searchpos = threatpos.Clone()
			searchpos, err = threatpos.MovePreallocated(&move, searchpos)
			if err == nil {
				over, winner := searchpos.GameOver()
				if over && threatpos.ToMove() == winner {
					threatmoves = append(threatmoves, move)
				}
			}
		}
	}
	allmoves = []tak.Move{}
	allmoves = p.AllMoves(allmoves)
	allmovesvalue := []float64{}
	movecount := float64(0)
	valuesum := int64(0)
	valuesum2 := int64(0)
	for _, move := range allmoves {
		searchpos, err = p.MovePreallocated(&move, searchpos)
		if err != nil {
			//fmt.Printf("Illegal move found:\n%+v\n", err)
			allmovesvalue = append(allmovesvalue, float64(Illegal))
		} else {
			searchpos.Threatmoves = threatmoves
			value := -m.evaluate(m, searchpos)
			allmovesvalue = append(allmovesvalue, float64(value))
			deviationvalue := value
			if deviationvalue > 7000 {
				deviationvalue = 7000
			} else if deviationvalue < -7000 {
				deviationvalue = -7000
			}
			movecount++
			valuesum += deviationvalue
			valuesum2 += deviationvalue * deviationvalue
		}
	}
	stddev := math.Sqrt((float64(valuesum2) - float64(valuesum*valuesum)/movecount) / movecount)
	//fmt.Printf("Std dev:\n%+v\n", stddev)
	if stddev < 40 {
		stddev = 40
	}
	if stddev > spread {
		stddev = spread
	}
	moveid := int(0)
	movevalue := float64(Illegal)
	for id, value := range allmovesvalue {
		rndvalue := value + m.rand.Float64()*stddev*1
		if rndvalue > movevalue {
			movevalue = rndvalue
			moveid = id
		}
	}
	//fmt.Printf("All moves values:\n%+v\n", allmovesvalue)
	//fmt.Printf("Chosen move value:\n%+v\n", allmovesvalue[moveid])
	//fmt.Printf("Threat moves:\n%+v\n", threatmoves)
	return allmoves[moveid], movevalue
}

func (m *MinimaxAI) GetMovex(ctx context.Context, p *tak.Position, depth int, spread float64) (tak.Move, float64) {
	depth--
	if depth==0 {
		retmove, retvalue := m.GetMove1(ctx, p, 0)
		return retmove, -retvalue
	} else {
		allmoves := []tak.Move{}
		allmoves = p.AllMoves(allmoves)
		var searchpos *tak.Position
		var err error
		//allmovesvalue := []float64{}
		var bestmove tak.Move
		movevalue := float64(Illegal)
		for _, move := range allmoves {
			searchpos, err = p.MovePreallocated(&move, searchpos)
			if err != nil {
				//fmt.Printf("Illegal move found:\n%+v\n", err)
				//allmovesvalue = append(allmovesvalue, float64(Illegal))
			} else {
				over, winner := searchpos.GameOver()
				var value float64
				if over {
					switch winner {
					case tak.NoColor:
						value = 0
					case p.ToMove():
						value = float64(MaxEval) - float64(p.MoveNumber())*1000000
					default:
						value = float64(MinEval) + float64(p.MoveNumber())*1000000
					}
				} else {
					_, value = m.GetMovex(ctx, searchpos, depth, 0)
					//allmovesvalue = append(allmovesvalue, float64(value))
				}
				value += m.rand.Float64()*spread
				if value > movevalue {
					movevalue = value
					bestmove = move
				}
			}
		}
		return bestmove, -movevalue
	}
}

func (m *MinimaxAI) GetMove(ctx context.Context, p *tak.Position) tak.Move {
	if m.cfg.Depth == 1 {
		move, _ := m.GetMove1(ctx, p, 100)
		return move
	} else {
		/*move, _ := m.GetMovex(ctx, p, m.cfg.Depth, 200)
		return move*/
		ms, _, _ := m.Analyze(ctx, p)
		return ms[0]
	}
}


func (m *MinimaxAI) Analyze(ctx context.Context, p *tak.Position) ([]tak.Move, int64, Stats) {
	if m.cfg.Size != p.Size() {
		panic("Analyze: wrong size")
	}
	for i, v := range m.history {
		m.history[i] = v / 2
	}
	var cancel int32
	m.cancel = &cancel
	go func() {
		<-ctx.Done()
		atomic.StoreInt32(&cancel, 1)
	}()

	var seed = m.cfg.Seed
	if seed == 0 {
		seed = time.Now().Unix()
	}
	m.rand = rand.New(rand.NewSource(seed))
	if m.cfg.Debug > 0 {
		log.Printf("start search ply=%d color=%s seed=%d",
			p.MoveNumber(), p.ToMove(), seed)
	}
	deadline, limited := ctx.Deadline()

	var next []tak.Move
	ms := make([]tak.Move, 0, maxDepth)
	var v int64
	top := time.Now()
	var prevEval uint64
	var branchSum uint64
	base := 0
	te := m.ttGet(p.Hash())
	if te != nil && te.bound == exactBound {
		base = te.depth
		ms = append(ms[:0], te.m)
	}

	for i := 1; i+base <= m.cfg.Depth; i++ {
		m.st = Stats{Depth: i + base}
		start := time.Now()
		next, v = m.minimax(p, 0, i+base, ms, MinEval-1, MaxEval+1, 0)
		if next == nil || atomic.LoadInt32(m.cancel) != 0 {
			break
		}
		ms = append(ms[:0], next...)
		timeUsed := time.Now().Sub(top)
		timeMove := time.Now().Sub(start)
		if m.cfg.Debug > 0 {
			log.Printf("[minimax] deepen: depth=%d val=%d pv=%s time=%s total=%s evaluated=%d tt=%d/%d branch=%d",
				base+i, v, formatpv(ms),
				timeMove,
				timeUsed,
				m.st.Evaluated,
				m.st.TTShortcut,
				m.st.TTHits,
				m.st.Evaluated/(prevEval+1),
			)
		}
		if m.cfg.Debug > 1 {
			log.Printf("[minimax]  stats: visited=%d scout=%d evaluated=%d null=%d/%d cut=%d cut0=%d(%2.2f) cut1=%d(%2.2f) m/cut=%2.2f m/ms=%f all=%d research=%d",
				m.st.Visited,
				m.st.Scout,
				m.st.Evaluated,
				m.st.NullCut,
				m.st.NullSearch,
				m.st.CutNodes,
				m.st.Cut0,
				float64(m.st.Cut0)/float64(m.st.CutNodes+1),
				m.st.Cut1,
				float64(m.st.Cut0+m.st.Cut1)/float64(m.st.CutNodes+1),
				float64(m.st.CutSearch)/float64(m.st.CutNodes-m.st.Cut0-m.st.Cut1+1),
				float64(m.st.Visited+m.st.Evaluated)/float64(timeMove.Seconds()*1000),
				m.st.AllNodes,
				m.st.ReSearch)
		}
		if i > 1 {
			branchSum += m.st.Evaluated / (prevEval + 1)
		}
		prevEval = m.st.Evaluated
		if v > WinThreshold || v < -WinThreshold {
			break
		}
		if limited && i+base != m.cfg.Depth {
			var branch uint64
			if i > 2 {
				// conservatively multiply by 2 to
				// account for the bimodal branching
				// factor
				branch = 2 * branchSum / uint64(i-1)
			} else {
				// conservative estimate if we haven't
				// run enough plies to have one
				// yet. This can matter if the table
				// returns a deep move
				branch = 20
			}
			estimate := time.Now().Add(time.Now().Sub(start) * time.Duration(branch))
			if estimate.After(deadline) {
				if m.cfg.Debug > 0 {
					log.Printf("[minimax] time cutoff: depth=%d used=%s estimate=%s",
						i, timeUsed, estimate.Sub(top))
				}
				break
			}
		}
	}
	return ms, v, m.st
}

func (ai *MinimaxAI) minimax(
	p *tak.Position,
	ply, depth int,
	pv []tak.Move,
	α, β int64,
	diverseadd int64) ([]tak.Move, int64) {
	diverseaddvar := diverseadd
	over, _ := p.GameOver()
	if depth == 0 || over {
		ai.st.Evaluated++
		if over {
			ai.st.Terminal++
		}
		return nil, ai.evaluate(ai, p)+diverseadd
	}

	ai.st.Visited++
	if β == α+1 {
		ai.st.Scout++
	}

	te := ai.ttGet(p.Hash())
	if te != nil {
		ai.st.TTHits++
		teSuffices := false
		if te.depth >= depth {
			if te.bound == exactBound ||
				(te.value < α && te.bound == upperBound) ||
				(te.value > β && te.bound == lowerBound) {
				teSuffices = true
			}
		}

		if te.bound == exactBound &&
			(te.value > WinThreshold || te.value < -WinThreshold) {
			teSuffices = true
		}
		if teSuffices {
			_, e := p.MovePreallocated(&te.m, ai.stack[ply].p)
			if e == nil {
				ai.st.TTShortcut++
				ai.stack[ply].pv[0] = te.m
				return ai.stack[ply].pv[:1], te.value
			}
			te = nil
		}
	}

	if β == α+1 && ai.nullMoveOK(ply, depth, p) {
		ai.stack[ply].m = tak.Move{Type: tak.Pass}
		child, e := p.MovePreallocated(&ai.stack[ply].m, ai.stack[ply].p)
		if e == nil {
			ai.st.NullSearch++
			if(diverseadd==0){
				diverseaddvar = 1+ai.rand.Int63n(1+ai.Diversify)
			}
			_, v := ai.minimax(child, ply+1, depth-3, nil, -α-1, -α, diverseaddvar)
			v = -v
			if v >= β {
				ai.st.NullCut++
				return nil, v
			}
		}
	}

	// As of 1.6.2, Go's escape analysis can't tell that a
	// stack-allocated object here doesn't escape. So we force it
	// into our manual stack.
	mg := &ai.stack[ply].mg
	*mg = moveGenerator{
		ai:    ai,
		ply:   ply,
		depth: depth,
		p:     p,
		te:    te,
		pv:    pv,
	}

	best := ai.stack[ply].pv[:0]
	best = append(best, pv...)
	improved := false
	var i int
	var threats []tak.Move
	if(depth==1){
		threats = getThreats(p)
	}
	for m, child := mg.Next(); child != nil; m, child = mg.Next() {
		i++
		var ms []tak.Move
		var newpv []tak.Move
		var v int64
		if len(best) != 0 {
			newpv = best[1:]
		}
		ai.stack[ply].m = m
		//fmt.Printf("%+v\n\n", m)
		if(diverseadd==0){
			diverseaddvar = 1+ai.rand.Int63n(1+ai.Diversify)
		}
		if(depth==1){
			child.Threatmoves=threats
		}
		if i > 1 {
			ms, v = ai.minimax(child, ply+1, depth-1, newpv, -α-1, -α, diverseaddvar)
			if -v > α && -v < β {
				ai.st.ReSearch++
				ms, v = ai.minimax(child, ply+1, depth-1, newpv, -β, -α, diverseaddvar)
			}
		} else {
			ms, v = ai.minimax(child, ply+1, depth-1, newpv, -β, -α, diverseaddvar)
		}
		v = -v

		if len(best) == 0 {
			best = append(best[:0], m)
			best = append(best, ms...)
		}
		if v > α {
			improved = true
			best = append(best[:0], m)
			best = append(best, ms...)
			α = v
			if α >= β {
				ai.st.CutNodes++
				switch i {
				case 1:
					ai.st.Cut0++
				case 2:
					ai.st.Cut1++
				default:
					ai.st.CutSearch += uint64(i + 1)
				}
				ai.history[m.Hash()] += (1 << uint(depth))
				if ply > 0 {
					ai.response[ai.stack[ply-1].m.Hash()] = m
				}
				if ai.cfg.Debug > 3 && i > 20 && depth >= 3 {
					var tm tak.Move
					td := 0
					if te != nil {
						tm = te.m
						td = te.depth
					}
					log.Printf("[minimax] late cutoff depth=%d m=%d pv=%s te=%d:%s killer=%s pos=%q",
						depth, i, formatpv(pv), td, ptn.FormatMove(&tm), ptn.FormatMove(&m), ptn.FormatTPS(p),
					)
				}
				break
			}
		}
		if atomic.LoadInt32(ai.cancel) != 0 {
			return nil, 0
		}
	}

	if te = ai.ttPut(p.Hash()); te != nil {
		te.hash = p.Hash()
		te.depth = depth
		te.m = best[0]
		te.value = α
		if !improved {
			te.bound = upperBound
			ai.st.AllNodes++
		} else if α >= β {
			te.bound = lowerBound
		} else {
			te.bound = exactBound
		}
	}

	return best, α
}

func (ai *MinimaxAI) nullMoveOK(ply, depth int, p *tak.Position) bool {
	if ai.cfg.NoNullMove {
		return false
	}
	if ply == 0 || depth < 3 {
		return false
	}
	if ai.stack[ply-1].m.Type == tak.Pass {
		return false
	}
	if p.WhiteStones() < 3 || p.BlackStones() < 3 {
		return false
	}
	if bitboard.Popcount(p.White|p.Black)+3 >= len(p.Stacks) {
		return false
	}
	return true
}
