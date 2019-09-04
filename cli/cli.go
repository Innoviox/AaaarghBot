package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"../ptn"
	"../tak"
)

type Player interface {
	GetMove(p *tak.Position) tak.Move
}

type CLI struct {
	moves []tak.Move
	p     *tak.Position

	Config tak.Config
	Out    io.Writer
	White  Player
	Black  Player
	
	Silent bool
}

func (c *CLI) Play() *tak.Position {
	c.moves = nil
	c.p = tak.New(c.Config)
	for {
		if !c.Silent {
			c.render()
		}
		if ok, _ := c.p.GameOver(); ok {
			d := c.p.WinDetails()
			if !c.Silent {
				fmt.Fprintf(c.Out, "Game Over! ")
				if d.Winner == tak.NoColor {
					fmt.Fprintf(c.Out, "Draw.")
				} else {
					fmt.Fprintf(c.Out, "%s wins by ", d.Winner)
					switch d.Reason {
					case tak.RoadWin:
						fmt.Fprintf(c.Out, "building a road")
					case tak.FlatsWin:
						fmt.Fprintf(c.Out, "flats count")
					}
				}
				fmt.Fprintf(c.Out, "\nflats count: white=%d black=%d\n",
					d.WhiteFlats,
					d.BlackFlats)
			}
			return c.p
		}
		if c.p.MoveNumber()>200{
			return c.p
		}
		var m tak.Move
		if c.p.ToMove() == tak.White {
			m = c.White.GetMove(c.p)
		} else {
			m = c.Black.GetMove(c.p)
		}
		p, e := c.p.Move(&m)
		if e != nil {
			fmt.Fprintln(c.Out, "illegal move:", e)
		} else {
			if !c.Silent {
				if c.p.ToMove() == tak.White {
					fmt.Fprintf(c.Out, "%d. %s", c.p.MoveNumber()/2+1, ptn.FormatMove(&m))
				} else {
					fmt.Fprintf(c.Out, "%d. ... %s", c.p.MoveNumber()/2+1, ptn.FormatMove(&m))
				}
			}
			c.p = p
			c.moves = append(c.moves, m)
		}
	}
}

func (c *CLI) Moves() []tak.Move {
	return c.moves
}

func (c *CLI) render() {
	RenderBoard(c.Out, c.p)
}

func RenderBoard(out io.Writer, p *tak.Position) {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "[%s to play]\n", p.ToMove())
	w := tabwriter.NewWriter(out, 4, 8, 1, '\t', 0)
	for y := p.Size() - 1; y >= 0; y-- {
		fmt.Fprintf(w, "%c.\t", '1'+y)
		for x := 0; x < p.Size(); x++ {
			fmt.Fprintf(w, "%v\t", p.At(x, y))
		}
		fmt.Fprintf(w, "\n")
	}
	fmt.Fprintf(w, "\t")
	for x := 0; x < p.Size(); x++ {
		fmt.Fprintf(w, "%c.\t", 'a'+x)
	}
	fmt.Fprintf(w, "\n")
	w.Flush()
	fmt.Fprintf(out, "stones: W:%d B:%d\n", p.WhiteStones(), p.BlackStones())
}
