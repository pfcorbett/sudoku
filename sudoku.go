// Sudoku.go
//
// This program will solve a Sudoku using the same techniques a human uses to solve Sudoku.  I.e., instead of doing a breadth first or depth first search of the possible
// solution space, it will at each step only commit numbers to squares when that number is provably correct.
// It uses channels and goroutines to constantly monitor as decisions are made and whether those decisions allow further choices to be made
package main

import (
	"fmt"
	"math/bits"
	"os"
	"sync"
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

const sleep_msec = 10
const max_bufferchan = 40 * 20
const max_inchan = 50

type Action int

const (
	set Action = iota
	clear
	pause
	analyseRow
	analyseCol
	analyseBox
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
var wgRound sync.WaitGroup

var wgSqrsDone sync.WaitGroup
var wgThrdsDone sync.WaitGroup
var wgRCB sync.WaitGroup

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Insufficient args, missing input filename.\n")
		os.Exit(1)
	}
	abortChan = make(chan struct{})
	wgRound.Add(9 * 9)
	wgSqrsDone.Add(9 * 9)
	wgThrdsDone.Add(9*9 + 1)
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
	go roundLooper()

	err := captureBoard(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	wgSqrsDone.Wait()
	//close(abortChan)
	close(bufferChan)
	wgThrdsDone.Wait()
}

func roundLooper() {
	forwardMsgs := func() {
		// Drain the buffer channel and forward the next round messages to the waiting workers
		// First check capacity
		cnt := len(bufferChan)
		if cnt == cap(bufferChan) {
			panic("buffer channel is full, this is bad")
		}

		// Forward all the enqueued messages
		for msg := range bufferChan {
			board[msg.destR][msg.destC].inChan <- msg
			cnt--
			if cnt == 0 {
				break
			}
		}
	}

	pauseMonitors := func() {
		for i := 0; i < 9; i++ {
			for j := 0; j < 9; j++ {
				board[i][j].inChan <- UpdateMsg{action: pause}
			}
		}
		wgRound.Wait()
		wgRound.Add(81)
	}

	abortFlag := false
	go func() {
		wgSqrsDone.Wait()
		abortFlag = true
	}()

	wgRound.Wait()  // All square monitor goroutines have quiesced.
	wgRound.Add(81) // Reset the worker wait group for the next round
	//loop:
	for !abortFlag {
		// Collect messages from each round, wait for each round to quiesce, then distribute messages to next round
		displayBoard()
		forwardMsgs()
		pauseMonitors()
		wgRCB.Add(27)
		inspectRCB()
		wgRCB.Wait()
		forwardMsgs()
		pauseMonitors()
	}
	displayBoard()
	wgThrdsDone.Done()
	close(abortChan)
}

func inspectRCB() {
	for i := 0; i < 9; i++ {
		board[i][i].inChan <- UpdateMsg{action: analyseRow}
	}
	for i := 0; i < 9; i++ {
		board[i][(i+1)%9].inChan <- UpdateMsg{action: analyseCol}
	}
	for i := 0; i < 9; i += 3 {
		for j := 2; j < 9; j += 3 {
			board[i][j].inChan <- UpdateMsg{action: analyseBox}
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
						// WaitGroup 3 triggers completion of sudoku when all squares have been finalized
						wgSqrsDone.Add(-1)
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
						// WaitGroup 3 triggers completion of sudoku when all squares have been finalized
						wgSqrsDone.Add(-1)
					}
				}
			case pause:
				wgRound.Done() // Waitgroup 1 tracks the number of squares that are still active in this round.
			case analyseRow:
				inspectRow(i, j)
				wgRCB.Done()
			case analyseCol:
				inspectCol(i, j)
				wgRCB.Done()
			case analyseBox:
				inspectBox(i, j)
				wgRCB.Done()
			default:
				panic("Should always have an action")
			}
		case <-abortChan:
			// Global abort signal received (via main closing the abortChan)
			if !sqr.isFinal {
				panic("should not get here if wg is zero")
			}
			wgThrdsDone.Done()
			break outerloop
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
	// Count and locate each possible number in the remaining squares
	colPos := make(map[squareVal][]int)
	unplacedValues := blank
	for val := one; val <= nine; val <<= 1 {
		for j := 0; j < 9; j++ {
			if board[r][j].possVal&val == val {
				// square could be this value
				colPos[val] = append(colPos[val], j)
			}
		}
		if len(colPos[val]) == 0 {
			panic("this is a problem, number not found in row")
		}
		// Check for previously unknown singletons in the row
		if len(colPos[val]) == 1 {
			unplacedValues &^= val
			cPos := colPos[val][0]
			if !board[r][cPos].isFinal {
				bufferChan <- UpdateMsg{val, set, r, cPos}
			}
		} else {
			// Check if all possible locations for the number are within the same box
			cPosLow := colPos[val][0]
			cPosLast := len(colPos[val]) - 1
			cPosHigh := colPos[val][cPosLast]
			if cPosLow/3 == cPosHigh/3 {
				// All instances of the number are in the same box.
				cb := cPosLow / 3 * 3
				rb := r / 3 * 3
				for ri := rb; ri < rb+3; ri++ {
					if ri == r {
						continue
					}
					for ci := cb; ci < cb+3; ci++ {
						bufferChan <- UpdateMsg{val, clear, ri, ci}
					}
				}
			}
		}
	}
	// Do some harder Sudoku solving.
	checkConstrainedSquares(unplacedValues, r, true, colPos)
	checkConstrainedValues(r, true)
}

func inspectCol(r, c int) {
	// Count and locate each possible number in the remaining squares
	rowPos := make(map[squareVal][]int)
	unplacedValues := blank
	for val := one; val <= nine; val <<= 1 {
		for i := 0; i < 9; i++ {
			if board[i][c].possVal&val == val {
				// square could be this value
				rowPos[val] = append(rowPos[val], i)
			}
		}
		if len(rowPos[val]) == 0 {
			panic("this is a problem, number not found in column")
		}
		// Check for previously unknown singletons in the column
		if len(rowPos[val]) == 1 {
			unplacedValues &^= val
			rPos := rowPos[val][0]
			if !board[rPos][c].isFinal {
				bufferChan <- UpdateMsg{val, set, rPos, c}
			}
		} else {
			// Check if all possible locations for the number are within the same box
			rPosLow := rowPos[val][0]
			rPosLast := len(rowPos[val]) - 1
			rPosHigh := rowPos[val][rPosLast]
			if rPosLow/3 == rPosHigh/3 {
				// All instances of the number are in the same box.
				rb := rPosLow / 3 * 3
				cb := c / 3 * 3
				for ci := cb; ci < cb+3; ci++ {
					if ci == c {
						continue
					}
					for ri := rb; ri < rb+3; ri++ {
						bufferChan <- UpdateMsg{val, clear, ri, ci}
					}
				}
			}
		}
	}
	// Do some harder Sudoku solving.
	checkConstrainedSquares(unplacedValues, c, false, rowPos)
	checkConstrainedValues(c, false)
}

func checkConstrainedSquares(unplacedValues squareVal, rc int, isRow bool, rcPos map[squareVal][]int) {
	// If two values are only found in two squares, then those squares cannot have any other value.
	if bits.OnesCount16(uint16(unplacedValues)) > 2 {
		for val1 := one; val1 <= eight; val1 <<= 1 {
			if unplacedValues&val1 == 0 {
				continue
			}
			for val2 := val1 << 1; val2 <= nine; val2 <<= 1 {
				if unplacedValues&val2 == 0 {
					continue
				}
				posArray := make([]int, 0, 3)
				var posMap uint16
				cnt := 0
				for _, i := range rcPos[val1] {
					if cnt > 2 {
						break
					}
					if posMap&(1<<i) == 0 {
						posMap |= 1 << i
						posArray = append(posArray, i)
						cnt++
					}
				}
				for _, i := range rcPos[val2] {
					if cnt > 2 {
						break
					}
					if posMap&(1<<i) == 0 {
						posMap |= 1 << i
						posArray = append(posArray, i)
						cnt++
					}
				}
				if cnt == 2 {
					// These two values can only be placed in two squares.  Clear all other possible values of those squares.
					clearVal := blank &^ (val1 | val2)
					if isRow {
						bufferChan <- UpdateMsg{clearVal, clear, rc, posArray[0]}
						bufferChan <- UpdateMsg{clearVal, clear, rc, posArray[1]}
					} else {
						bufferChan <- UpdateMsg{clearVal, clear, posArray[0], rc}
						bufferChan <- UpdateMsg{clearVal, clear, posArray[1], rc}
					}
				}
			}
		}
	}

	// If three values are only found in three squares, then those squares cannot have any other value.
	if bits.OnesCount16(uint16(unplacedValues)) > 3 {
		for val1 := one; val1 <= seven; val1 <<= 1 {
			if unplacedValues&val1 == 0 {
				continue
			}
			for val2 := val1 << 1; val2 <= eight; val2 <<= 1 {
				if unplacedValues&val2 == 0 {
					continue
				}
				for val3 := val2 << 1; val3 <= nine; val3 <<= 1 {
					if unplacedValues&val2 == 0 {
						continue
					}
					posArray := make([]int, 0, 4)
					var posMap uint16
					cnt := 0
					for _, i := range rcPos[val1] {
						if cnt > 3 {
							break
						}
						if posMap&(1<<i) == 0 {
							posMap |= 1 << i
							posArray = append(posArray, i)
							cnt++
						}
					}
					for _, i := range rcPos[val2] {
						if cnt > 3 {
							break
						}
						if posMap&(1<<i) == 0 {
							posMap |= 1 << i
							posArray = append(posArray, i)
							cnt++
						}
					}
					for _, i := range rcPos[val3] {
						if cnt > 3 {
							break
						}
						if posMap&(1<<i) == 0 {
							posMap |= 1 << i
							posArray = append(posArray, i)
							cnt++
						}
					}
					if cnt == 3 {
						// These three values can only be placed in three squares.  Clear all other possible values of those squares.
						clearVal := blank &^ (val1 | val2 | val3)
						if isRow {
						} else {
							bufferChan <- UpdateMsg{clearVal, clear, posArray[0], rc}
							bufferChan <- UpdateMsg{clearVal, clear, posArray[1], rc}
							bufferChan <- UpdateMsg{clearVal, clear, posArray[2], rc}
						}
					}
				}
			}
		}
	}
}

func checkConstrainedValues(rc int, isRow bool) {
	// If two squares can only hold the same two values and no others, then clear those values from the rest of the row.
	var pvCnt [9]int
	var sqrPaired [9]bool
	unresolvedCnt := 0
	for j := 0; j < 9; j++ {
		if isRow {
			pvCnt[j] = bits.OnesCount16(uint16(board[rc][j].possVal))
		} else {
			pvCnt[j] = bits.OnesCount16(uint16(board[j][rc].possVal))
		}
		if pvCnt[j] >= 2 {
			unresolvedCnt++
		}
	}
	if unresolvedCnt > 2 {
		for j1 := 0; j1 < 8; j1++ {
			if pvCnt[j1] != 2 {
				continue
			}
			for j2 := j1 + 1; j2 < 9; j2++ {
				if pvCnt[j2] != 2 {
					continue
				}
				possVal1 := board[rc][j1].possVal
				possVal2 := board[rc][j2].possVal
				if !isRow {
					possVal1 = board[j1][rc].possVal
					possVal2 = board[j2][rc].possVal
				}
				if possVal1 == possVal2 {
					// We found a match of two squares that have the same two possible values. Clear those values from other squares in the row.
					sqrPaired[j1] = true
					sqrPaired[j2] = true
					for j := 0; j < 9; j++ {
						if j == j1 || j == j2 {
							continue
						}
						if isRow && board[rc][j].isFinal {
							continue
						}
						if !isRow && board[j][rc].isFinal {
							continue
						}
						r, c := rc, j
						if !isRow {
							r, c = j, rc
						}
						bufferChan <- UpdateMsg{possVal1, clear, r, c}
					}
				}
			}
		}
		// If three squares can only hold the same three values and no others, then clear those values from the rest of the row.
		if unresolvedCnt > 3 {
			for j1 := 0; j1 < 7; j1++ {
				if sqrPaired[j1] {
					continue
				}
				if pvCnt[j1] != 2 && pvCnt[j1] != 3 {
					continue
				}
				for j2 := j1 + 1; j2 < 8; j2++ {
					if sqrPaired[j2] {
						continue
					}
					if pvCnt[j2] != 2 && pvCnt[j2] != 3 {
						continue
					}
					for j3 := j2 + 1; j3 < 9; j3++ {
						if sqrPaired[j3] {
							continue
						}
						if pvCnt[j3] != 2 && pvCnt[j3] != 3 {
							continue
						}
						var mergeVal squareVal
						if isRow {
							mergeVal = board[rc][j1].possVal | board[rc][j2].possVal | board[rc][j3].possVal
						} else {
							mergeVal = board[j1][rc].possVal | board[j2][rc].possVal | board[j3][rc].possVal
						}
						if bits.OnesCount16(uint16(mergeVal)) == 3 {
							//We found a match of three unresolved squares that each have two or three the same possible three values
							for j := 0; j < 9; j++ {
								if j == j1 || j == j2 || j == j3 {
									continue
								}
								if isRow && board[rc][j].isFinal {
									continue
								}
								if !isRow && board[j][rc].isFinal {
									continue
								}
								r, c := rc, j
								if !isRow {
									r, c = j, rc
								}
								bufferChan <- UpdateMsg{mergeVal, clear, r, c}
							}
						}
					}
				}
			}
		}
	}
}

func inspectBox(r, c int) {
	type boxPosStruct struct {
		r int
		c int
	}
	// Count and locate each possible number in the remaining squares
	for val := one; val <= nine; val <<= 1 {
		boxRowPos := make(map[squareVal][]boxPosStruct)
		boxColPos := make(map[squareVal][]boxPosStruct)
		rb := r / 3 * 3
		cb := c / 3 * 3
		for i := rb; i < rb+3; i++ {
			for j := cb; j < cb+3; j++ {
				if board[i][j].possVal&val == val {
					// square could be this value
					boxRowPos[val] = append(boxRowPos[val], boxPosStruct{i, j})
				}
			}
		}
		for j := cb; j < cb+3; j++ {
			for i := rb; i < rb+3; i++ {
				if board[i][j].possVal&val == val {
					// square could be this value
					boxColPos[val] = append(boxColPos[val], boxPosStruct{i, j})
				}
			}
		}
		if len(boxRowPos[val]) != len(boxColPos[val]) {
			panic("these should be equal")
		}
		if len(boxRowPos[val]) == 0 {
			panic("this is a problem, number not found in box")
		}
		// Check for previously unknown singletons in the box
		if len(boxRowPos[val]) == 1 {
			rPos := boxRowPos[val][0].r
			cPos := boxRowPos[val][0].c
			if !board[rPos][cPos].isFinal {
				bufferChan <- UpdateMsg{val, set, rPos, cPos}
			}
		} else {
			// Check if all possible locations for the number are within the same row or column
			boxPosLast := len(boxRowPos[val]) - 1
			rPosLow := boxRowPos[val][0].r
			rPosHigh := boxRowPos[val][boxPosLast].r
			cPosLow := boxColPos[val][0].c
			cPosHigh := boxColPos[val][boxPosLast].c
			if rPosLow == rPosHigh {
				// All possible locations of the number in this box are in the same row.
				for ri := rb; ri < rb+3; ri++ {
					if ri == rPosLow {
						continue
					}
					for ci := cb; ci < cb+3; ci++ {
						bufferChan <- UpdateMsg{val, clear, ri, ci}
					}
				}
			}
			if cPosLow == cPosHigh {
				// All possible locations of the number in this box are in the same column.
				for ci := cb; ci < cb+3; ci++ {
					if ci == cPosLow {
						continue
					}
					for ri := rb; ri < rb+3; ri++ {
						bufferChan <- UpdateMsg{val, clear, ri, ci}
					}
				}
			}
		}
	}
}

func finalCheckVal(val squareVal) (rv bool) {
	if bits.OnesCount16(uint16(val)) == 1 {
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
