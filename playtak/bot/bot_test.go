package bot

import (
	"fmt"
	"testing"

	"../../playtak"
	"../../ptn"
	"../../tak"
)

func parseMoves(spec [][2]string) [][2]*tak.Move {
	var out [][2]*tak.Move
	for _, r := range spec {
		var o [2]*tak.Move
		for i, n := range r {
			if n == "" {
				continue
			}
			m, e := ptn.ParseMove(n)
			if e != nil {
				panic("bad ptn")
			}
			o[i] = &m
		}
		out = append(out, o)
	}
	return out
}

func appendMove(transcript []Expectation,
	id string, tm int,
	move [2]*tak.Move) []Expectation {
	transcript = append(transcript, Expectation{
		recv: []string{
			fmt.Sprintf("Game#%s %s", id, playtak.FormatServer(move[0])),
		},
	})
	if move[1] == nil {
		return transcript
	}
	transcript = append(transcript, Expectation{
		send: []string{
			fmt.Sprintf("Game#%s %s", id, playtak.FormatServer(move[1])),
			fmt.Sprintf("Game#%s Time %d %d", id, tm, tm),
		},
	})
	return transcript
}

const startLine = "Game Start 100 5 Taktician vs HonestJoe white 600"

var defaultGame = [][2]string{
	{"a1", "e1"},
	{"e3", "b1"},
	{"e2", "b2"},
	{"Ce4", "a2"},
	{"e5", ""},
}

func setupGame(spec [][2]string) (*TestBotStatic, []Expectation) {
	moves := parseMoves(spec)
	bot := &TestBotStatic{}
	for _, r := range moves {
		bot.moves = append(bot.moves, *r[0])
	}

	var transcript []Expectation
	tm := 600
	for _, r := range moves {
		transcript = appendMove(
			transcript, "100", tm, r)
		tm -= 10
	}
	transcript = append(transcript, Expectation{
		send: []string{
			"Game#100 Over R-0",
		},
	})
	return bot, transcript
}

func assertPosition(t *testing.T, p *tak.Position, expect string) {
	got := ptn.FormatTPS(p)
	if got != expect {
		t.Fatalf("wrong position=%q !=%q", got, expect)
	}
}

func TestBasicGame(t *testing.T) {
	bot, transcript := setupGame(defaultGame)
	c := NewTestClient(t, transcript)
	defer c.shutdown()
	PlayGame(c, bot, startLine)
	assertPosition(t, bot.game.Positions[len(bot.game.Positions)-1],
		`x4,1/x4,1C/x4,1/2,2,x2,1/2,2,x2,1 2 5`)
}

func TestUndoGame(t *testing.T) {
	base, transcript := setupGame(defaultGame)
	bot := &TestBotUndo{*base, 5}

	i := 6
	rest := transcript[i:]
	transcript = transcript[:i:i]
	e := transcript[i-1]
	transcript = append(transcript,
		Expectation{
			send: []string{
				"Game#100 RequestUndo",
			},
			recv: []string{
				"Game#100 RequestUndo",
			},
		},
		Expectation{
			send: []string{
				"Game#100 Undo",
			},
		},
		Expectation{
			send: e.send,
		},
	)
	transcript = append(transcript, rest...)

	c := NewTestClient(t, transcript)
	defer c.shutdown()
	PlayGame(c, bot, startLine)
	assertPosition(t, bot.game.Positions[len(bot.game.Positions)-1],
		`x4,1/x4,1C/x4,1/2,2,x2,1/2,2,x2,1 2 5`)
}

func TestThinker(t *testing.T) {
	base, transcript := setupGame(defaultGame)
	bot := &TestBotThinker{TestBotStatic: *base}
	bot.wg.Add(9)

	c := NewTestClient(t, transcript)
	defer c.shutdown()
	PlayGame(c, bot, startLine)
	assertPosition(t, bot.game.Positions[len(bot.game.Positions)-1],
		`x4,1/x4,1C/x4,1/2,2,x2,1/2,2,x2,1 2 5`)
}

func TestAbandon(t *testing.T) {
	base, transcript := setupGame(defaultGame)
	bot := &TestBotThinker{TestBotStatic: *base}
	transcript = append(transcript[:7:7], Expectation{
		send: []string{"Game#100 Abandoned."},
	})

	c := NewTestClient(t, transcript)
	defer c.shutdown()
	bot.wg.Add(8)
	PlayGame(c, bot, startLine)
	bot.wg.Wait()
}

func TestResume(t *testing.T) {
	base, transcript := setupGame(defaultGame)
	bot := &TestBotResume{*base}

	var resume []Expectation
	for i := 0; i < 4; i++ {
		resume = append(resume, Expectation{
			send: transcript[2*i].recv,
		})
		resume = append(resume, Expectation{
			send: transcript[2*i+1].send[:1],
		})
	}
	transcript = append(resume, transcript[8:]...)

	c := NewTestClient(t, transcript)
	defer c.shutdown()
	PlayGame(c, bot, startLine)
	assertPosition(t, bot.game.Positions[len(bot.game.Positions)-1],
		`x4,1/x4,1C/x4,1/2,2,x2,1/2,2,x2,1 2 5`)
}
