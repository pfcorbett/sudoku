// Sudoku.go
//
// This program will solve a Sudoku using the same techniques a human uses to solve Sudoku.  I.e., instead of doing a breadth first or depth first search of the possible
// solution space, it will at each step only commit numbers to squares when that number is provably correct.
// It uses channels and goroutines to constantly monitor as decisions are made and whether those decisions allow further choices to be made
package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

type squareVal uint16

const (
	one squareVal = 1 << iota
	two
	three
	four
	five
	six
	seven
	eight
	nine
)
const blank = one | two | three | four | five | six | seven | eight | nine

const maxpoll = 10
const sleep_msec = 10
const max_bufferchan = 81 * (20 + 5)
const max_inchan = 50

type Action int

const (
	set Action = iota
	clear
	pause
)

type UpdateMsg struct {
	val    squareVal
	action Action
	destR  int
	destC  int
}

type square struct {
	possVal squareVal
	inChan  chan UpdateMsg
	isFinal bool
}

var abortChan chan struct{}
var bufferChan chan UpdateMsg
var board [9][9]square
var wg1 sync.WaitGroup
var wg2 sync.WaitGroup
var wg3 sync.WaitGroup
var wg4 sync.WaitGroup

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Insufficient args, missing input filename.\n")
		os.Exit(1)
	}
	abortChan = make(chan struct{})
	wg1.Add(9 * 9)
	wg3.Add(9 * 9)
	wg4.Add(9*9 + 1)
	// sentCnt and rcvdCnt arrays should both be initialized to 0
	for i := 0; i < 9; i++ {
		for j := 0; j < 9; j++ {
			board[i][j].possVal = blank
			board[i][j].inChan = make(chan UpdateMsg, max_inchan)
			board[i][j].isFinal = false
			go squareMonitor(i, j)
		}
	}
	bufferChan = make(chan UpdateMsg, max_bufferchan)
	err := captureBoard(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	go roundLooper()
	wg3.Wait()
	close(abortChan)
	close(bufferChan)
	wg4.Wait()
}

func roundLooper() {
loop:
	for {
		// Collect messages from each round, wait for each round to quiesce, then distribute messages to next round
		wg2.Add(1)  // Use this to block all workers after they've hit the quiesce point
		wg1.Wait()  // All square monitor goroutines have quiesced.
		wg1.Add(81) // Reset the worker wait group for the next round
		wg2.Done()  // Release the workers to start the next round

		displayBoard()
		// Check the abort channel to see if we should stop
		select {
		case <-abortChan:
			for i := 0; i < 9; i++ {
				for j := 0; j < 9; j++ {
					close(board[i][j].inChan)
				}
			}
			wg4.Done()
			break loop
		default:
			// do nothing, make select non-blocking
		}
		// Now we drain the buffer channel and forward the next round messages to the waiting workers
		// First check capacity
		cnt := len(bufferChan)
		if cnt == cap(bufferChan) {
			panic("buffer channel is full, this is bad")
		}
		if cnt == 0 {
			// Made no progress this round
			fmt.Printf("No progress in round")
		}
		// Forward all the enqueued messages
		for msg := range bufferChan {
			board[msg.destR][msg.destC].inChan <- msg
			cnt--
			if cnt == 0 {
				break
			}
		}
		for i := 0; i < 9; i++ {
			for j := 0; j < 9; j++ {
				board[i][j].inChan <- UpdateMsg{action: pause}
			}
		}
	}
}

func squareMonitor(i, j int) {
	sqr := &board[i][j]
outerloop:
	for {
		select {
		case msg := <-sqr.inChan:
			switch msg.action {
			case set:
				if sqr.isFinal {
					continue outerloop
				}
				if sqr.possVal != msg.val {
					sqr.possVal = msg.val
					if finalCheckVal(sqr.possVal) {
						sqr.isFinal = true
						sendUpdates(i, j, UpdateMsg{msg.val, clear, -1, -1})
						// Track squares that are finalized
						inspectRow(i, j)
						inspectCol(i, j)
						inspectBox(i, j)
						// WaitGroup 3 triggers completion of sudoku when all squares have been finalized
						wg3.Add(-1)
					}
				}
			case clear:
				if sqr.isFinal {
					continue outerloop
				}
				newval := sqr.possVal &^ msg.val
				if newval == sqr.possVal {
					// no change to square value
					continue
				} else {
					sqr.possVal = newval
					if finalCheckVal(sqr.possVal) {
						sqr.isFinal = true
						sendUpdates(i, j, UpdateMsg{newval, clear, -1, -1})
						// Track squares that are finalized
						inspectRow(i, j)
						inspectCol(i, j)
						inspectBox(i, j)
						// WaitGroup 3 triggers completion of sudoku when all squares have been finalized
						wg3.Add(-1)
					}
				}
			case pause:
				wg1.Done() // Waitgroup 1 tracks the number of squares that are still active in this round.
				wg2.Wait() // Waitgroup 2 is used to restart all the square monitor threads at once in a new round.  It toggles between 1 and 0
			default:
				panic("Should always have an action")
			}
		case <-abortChan:
			// Global abort signal received (via main closing the abortChan)
			wg1.Done()
			wg4.Done()
			if !sqr.isFinal {
				panic("should not get here if wg is zero")
			}
			break outerloop
		default:
			// Nothing coming in, sleep for a bit
			time.Sleep(time.Millisecond * sleep_msec)
		}
	}
}

func sendUpdates(r, c int, msg UpdateMsg) {
	// Update the rest of the row
	for j := 0; j < 9; j++ {
		if j == c {
			continue
		} else {
			if !board[r][j].isFinal {
				// The isFinal check is an optimization to reduce the number of messages sent to finalized squares.  No lock needed on board[r][j]
				msg.destR = r
				msg.destC = j
				bufferChan <- msg
			}
		}
	}
	// Update the rest of the column
	for i := 0; i < 9; i++ {
		if i == r {
			continue
		} else {
			if !board[i][c].isFinal {
				msg.destR = i
				msg.destC = c
				bufferChan <- msg
			}
		}
	}
	// Update the remainder of the block (not in the same row or column as the sending square)
	rb := r / 3 * 3
	cb := c / 3 * 3
	for i := rb; i < rb+3; i++ {
		for j := cb; j < cb+3; j++ {
			if i == r || j == c {
				// We have already notified squares in the same row and column
				continue
			} else {
				if !board[i][j].isFinal {
					msg.destR = i
					msg.destC = j
					bufferChan <- msg
				}
			}
		}
	}
}

func inspectRow(r, c int) {
	for val := one; val <= nine; val <<= 1 {
		cnt := 0
		cpos := -1
		for j := 0; j < 9; j++ {
			if board[r][j].possVal&val == val {
				cnt++
				cpos = j
			}
		}
		if cnt == 1 && !board[r][cpos].isFinal {
			bufferChan <- UpdateMsg{val, set, r, cpos}
		}
	}
}

func inspectCol(r, c int) {
	for val := one; val <= nine; val <<= 1 {
		cnt := 0
		rpos := -1
		for i := 0; i < 9; i++ {
			if board[i][c].possVal&val == val {
				cnt++
				rpos = i
			}
		}
		if cnt == 1 && !board[rpos][c].isFinal {
			bufferChan <- UpdateMsg{val, set, rpos, c}
		}
	}
}

func inspectBox(r, c int) {
	for val := one; val <= nine; val <<= 1 {
		cnt := 0
		rpos := -1
		cpos := -1
		rb := r / 3 * 3
		cb := c / 3 * 3
		for i := 0; i < 3; i++ {
			for j := 0; j < 3; j++ {
				if board[rb+i][cb+j].possVal&val == val {
					cnt++
					rpos = rb + i
					cpos = cb + j
				}
			}
		}
		if cnt == 1 && !board[rpos][cpos].isFinal {
			bufferChan <- UpdateMsg{val, set, rpos, cpos}
		}
	}
}

func finalCheckVal(val squareVal) (rv bool) {
	finalVal := map[squareVal]bool{
		one:   true,
		two:   true,
		three: true,
		four:  true,
		five:  true,
		six:   true,
		seven: true,
		eight: true,
		nine:  true,
	}
	if finalVal[val] {
		rv = true
	} else {
		rv = false
	}
	return
}

func captureBoard(inFileName string) error {
	inFile, err := os.Open(inFileName)
	if err != nil {
		return fmt.Errorf("Unable to open file %s: %v", inFileName, err)
	}
	for i := 0; i < 9; i++ {
		var iv [9]int

		intToVal := [...]squareVal{blank, one, two, three, four, five, six, seven, eight, nine}

		n, err := fmt.Fscanf(inFile, "%d,%d,%d;%d,%d,%d;%d,%d,%d;\n", &iv[0], &iv[1], &iv[2], &iv[3], &iv[4], &iv[5], &iv[6], &iv[7], &iv[8])
		if err != nil {
			return fmt.Errorf("Error reading file %s: %v", inFileName, err)
		}
		if n != 9 {
			return fmt.Errorf("Insufficient input line %d", i)
		}
		for j := 0; j < 9; j++ {
			if iv[j] < 0 || iv[j] > 9 {
				return fmt.Errorf("Invalid input line %d, position %d", i, j)
			} else {
				board[i][j].inChan <- UpdateMsg{intToVal[iv[j]], set, i, j}
				board[i][j].inChan <- UpdateMsg{action: pause}
			}
		}
	}
	return nil
}

func displayBoard() {
	var valToStr = map[squareVal]string{
		one:   "1",
		two:   "2",
		three: "3",
		four:  "4",
		five:  "5",
		six:   "6",
		seven: "7",
		eight: "8",
		nine:  "9",
		blank: " ",
	}
	displaySquare := func(v squareVal) (s string) {
		s = valToStr[v]
		if s == "" {
			s = " "
		}
		return
	}

	fmt.Println("\u250F\u2501\u2501\u2501\u252F\u2501\u2501\u2501\u252F\u2501\u2501\u2501\u2533\u2501\u2501\u2501\u252F\u2501\u2501\u2501" +
		"\u252F\u2501\u2501\u2501\u2533\u2501\u2501\u2501\u252F\u2501\u2501\u2501\u252F\u2501\u2501\u2501\u2513")
	for i := 0; i < 9; i++ {
		fmt.Printf("\u2503 %s \u2502 %s \u2502 %s \u2503 %s \u2502 %s \u2502 %s \u2503 %s \u2502 %s \u2502 %s \u2503\n",
			displaySquare(board[i][0].possVal),
			displaySquare(board[i][1].possVal),
			displaySquare(board[i][2].possVal),
			displaySquare(board[i][3].possVal),
			displaySquare(board[i][4].possVal),
			displaySquare(board[i][5].possVal),
			displaySquare(board[i][6].possVal),
			displaySquare(board[i][7].possVal),
			displaySquare(board[i][8].possVal))
		if i == 2 || i == 5 {
			fmt.Println("\u2523\u2501\u2501\u2501\u253F\u2501\u2501\u2501\u253F\u2501\u2501\u2501\u254B\u2501\u2501\u2501\u253F\u2501\u2501\u2501" +
				"\u253F\u2501\u2501\u2501\u254B\u2501\u2501\u2501\u253F\u2501\u2501\u2501\u253F\u2501\u2501\u2501\u252B")
		} else if i == 8 {
			fmt.Println("\u2517\u2501\u2501\u2501\u2537\u2501\u2501\u2501\u2537\u2501\u2501\u2501\u253B\u2501\u2501\u2501\u2537\u2501\u2501\u2501" +
				"\u2537\u2501\u2501\u2501\u253B\u2501\u2501\u2501\u2537\u2501\u2501\u2501\u2537\u2501\u2501\u2501\u251B")
		} else {
			fmt.Println("\u2520\u2500\u2500\u2500\u253C\u2500\u2500\u2500\u253C\u2500\u2500\u2500\u2542\u2500\u2500\u2500\u253C\u2500\u2500" +
				"\u2500\u253C\u2500\u2500\u2500\u2542\u2500\u2500\u2500\u253C\u2500\u2500\u2500\u253C\u2500\u2500\u2500\u2528")
		}
	}
}
