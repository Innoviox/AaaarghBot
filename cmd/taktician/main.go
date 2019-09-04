package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"../../playtak"
	"../../playtak/bot"
)

var (
	server    = flag.String("server", "playtak.com:10000", "playtak.com server to connect to")
	user      = flag.String("user", "AaaarghBot", "username for login")
	pass      = flag.String("pass", "", "password for login")
	accept    = flag.String("accept", "", "accept a game from specified user")
	gameTime  = flag.Duration("time", 1*time.Minute, "Length of game to offer")
	increment = flag.Duration("increment", 5*time.Second, "time increment to offer")
	size      = flag.Int("size", 5, "size of game to offer")
	once      = flag.Bool("once", false, "play a single game and exit")
	takbot    = flag.String("takbot", "", "challenge TakBot AI")

	friendly = flag.Bool("friendly", false, "play as FriendlyBot")

	debug           = flag.Int("debug", 1, "debug level")
	depth           = flag.Int("depth", 3, "minimax depth")
	limit           = flag.Duration("limit", time.Minute, "time limit per move")
	sort            = flag.Bool("sort", false, "sort moves via history heuristic")
	table           = flag.Bool("table", false, "use the transposition table")
	useOpponentTime = flag.Bool("use-opponent-time", false, "think on opponent's time")

	debugClient = flag.Bool("debug-client", false, "log debug output for playtak connection")
)

const ClientName = "Nohat AI"

func main() {
	flag.Parse()
	if *accept != "" || *takbot != "" {
		*once = true
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)

	backoff := 1 * time.Second
	var b bot.Bot
	for {
		client := &playtak.Client{
			Debug: *debugClient,
		}
		err := client.Connect(*server)
		if err != nil {
			goto reconnect
		}
		backoff = time.Second
		client.SendClient(ClientName)
		if *user != "" {
			err = client.Login(*user, *pass)
		} else {
			err = client.LoginGuest()
		}
		if err != nil {
			log.Fatal("login: ", err)
		}
		log.Printf("login OK")
		if *friendly {
			b = &Friendly{client: client}
		} else {
			b = &Taktician{client: client}
		}
		for {
			if *accept == "" {
				client.SendCommand("Seek",
					strconv.Itoa(*size),
					strconv.Itoa(int(gameTime.Seconds())),
					strconv.Itoa(int(increment.Seconds())))
				log.Printf("Seek OK")
				if *takbot != "" {
					client.SendCommand("Shout", "takbot: play", *takbot)
				}
			}

		recvLoop:
			for {
				select {
				case line, ok := <-client.Recv():
					if !ok {
						break recvLoop
					}
					if strings.HasPrefix(line, "Seek new") {
						bits := strings.Split(line, " ")
						if bits[3] == *accept {
							log.Printf("accepting game %s from %s", bits[2], bits[3])
							client.SendCommand("Accept", bits[2])
							break
						}
					}
					if strings.HasPrefix(line, "Game Start") {
						bot.PlayGame(client, b, line)
						break recvLoop
					}
					if strings.HasPrefix(line, "Shout") {
						who, msg := playtak.ParseShout(line)
						if who != "" {
							b.HandleChat(who, msg)
						}
					}
				case <-sigs:
					return
				}
			}
			if *once {
				return
			}
			if client.Error() != nil {
				log.Printf("Disconnected: %v", client.Error())
				break
			}
		}
	reconnect:
		log.Printf("sleeping %s before reconnect...", backoff)
		select {
		case <-time.After(backoff):
		case <-sigs:
			return
		}
		backoff = backoff * 2
		if backoff > time.Minute {
			backoff = time.Minute
		}
	}
}
