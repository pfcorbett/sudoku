// Sudoku.go
// © Peter Corbett, 2020
//
// This program will solve a Sudoku using the same techniques a human uses to solve Sudoku.  I.e., instead of doing a breadth first or depth first search of the possible
// solution space, it will at each step only commit numbers to squares when that number is provably correct, based entirely on what state has been deduced so far.
// The program is structured with a go rountine monitoring each of the 81 squares of the grid, one per square.  These respond to messages on an inbound channel.  The message actions
// are either state modifying, which are the set and clear actions.  Set is used to initially set the value of the square if it is known as an initial state (the squares that have numbers
// to seed the puzzle.).  It is also used when the value of a square has been determined to be one of the nine possible numbers.  The clear action is used to reduce the possible
// values a square may have.  Much of the logic of puzzle solving is to reduce the possible values of a square, eventually to a single value.  The values a square may have are stored as a bit
// vector in a single uint16, with each bit representing one of the values from 1 to 9, and the values represented in the program by appropriately named constants ("one", "two", etc.).  For
// squares that are not preset with a number as an initial condition, the special value "blank" is used as the first value of the square; it is simply the logical or of all the possible
// single number values, ie. 511 decimal, 0x1FF hex.
// Solving the sudoku involves several logic steps.
// 1. if a squares possible values have been reduced to one, then the square is finalized to that value.  The simplest way to exclude values is when one of the squares neighbors (in its row,
// column or block) has been finalized to have that value.  More complex cases occur when, for example, two squares in a row, column or block have been reduced to having the same two possible values;
// that excludes those values from the rest of the row, column or block.
// 2. if in a row, column, or block, there is only one place where a particular value can be placed.
// 3. if in a row, for example, the only place a value can be placed is within a group of three squares that are in the same block, then that value can not be placed elsewhere in that block.  The same
// holds in reverse, and the same holds between columns and blocks.
// 4. If two (or three) values are contrained to two (or three) squares in a row, column or block, then those two or three squares cannot hold any other value.
// 5. If two (or three) squares all hold the same two (or three) values and no other possible values, then those values cannot appear elsewhere in the row, column or block.
//
// The square monitor threads are the only threads that can change the value (or possible value) of a square, and they do so only at a presribed time, described as the beginning of a round.
// A round consists of a period where the square monitors process set and clear messages from their queues.  As they do that, they can send set and clear messages to other squares in their row, column or block.
// To avoid race conditions, the sent messages are sent first to a single channel, where they are queued for processing later in the round.
// The roundLooper go routine collects these messages.  The square monitors will process messages until they receive a pause message.  That tells them to release their hold on a waitgroup.  They then go back to
// listening on their incoming channel.  When the round looper wakes on the round counter waitgroup going to zero, it will forward all enqueued messages to the listening square monitors.  However, it will not forward
// additional messages as they arrive - it inspects the cnt of messages in its channel and forwards only that number.  It then sends a pause message to each square monitor.  When that phase of the round is complete,
// the round looper will send 27 messages to 27 of the square monitor threads, each of which initiates that thread to do analysis of one row, column or block.  This is where more complex scenarios are discovered, as
// described in 4 and 5 above.  Those threads will send new set and clear messages, which again a enqueued on the round loopers buffer channel, and are fowarded to the square monitors only after the waitgroup goes to zero.
//
// The entire program begins to wrap up once a waitgroup that counts the number of remaining unfinalized squares goes to zero.  At that point, an abort channel is closed, which acts as a broadcast to all threads to
// clean up and exit.  As each thread exits, it releases its hold on a thread count wait group.  All channels are closed.  When all threads except main have completed, main will complete and the program exits.
//
// Output to stdout is completed at the beginning when the puzzle's initial values are known, and at the completion of each round.  The output uses unicode characters to print a sudoku board.  The quality of this
// presentation depends on the character renderings in the terminal or printer, but for the most part, the boards are very legible.  An html output would possibly give a better rendering.
//
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

const maxBufferchan = 40 * 20
const maxInchan = 50

type action int

const (
	set action = iota
	clear
	pause
	analyseRow
	analyseCol
	analyseBlock
)

type rcbSelect int

const (
	row rcbSelect = iota
	column
	block
)

type updateMsg struct {
	val    squareVal
	action action
	destR  int
	destC  int
}

type square struct {
	possVal squareVal
	inChan  chan updateMsg
	isFinal bool
}

var abortChan chan struct{}
var bufferChan chan updateMsg
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
			board[i][j].inChan = make(chan updateMsg, maxInchan)
			board[i][j].isFinal = false
			go squareMonitor(i, j)
		}
	}
	bufferChan = make(chan updateMsg, maxBufferchan)
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
				board[i][j].inChan <- updateMsg{action: pause}
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
		board[i][i].inChan <- updateMsg{action: analyseRow}
	}
	for i := 0; i < 9; i++ {
		board[i][(i+1)%9].inChan <- updateMsg{action: analyseCol}
	}
	for i := 0; i < 9; i += 3 {
		for j := 2; j < 9; j += 3 {
			board[i][j].inChan <- updateMsg{action: analyseBlock}
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
						sendUpdates(i, j, updateMsg{msg.val, clear, -1, -1})
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
						sendUpdates(i, j, updateMsg{newval, clear, -1, -1})
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
			case analyseBlock:
				inspectBlock(i, j)
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

func sendUpdates(r, c int, msg updateMsg) {
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
				bufferChan <- updateMsg{val, set, r, cPos}
			}
		} else {
			// Check if all possible locations for the number are within the same block
			cPosLow := colPos[val][0]
			cPosLast := len(colPos[val]) - 1
			cPosHigh := colPos[val][cPosLast]
			if cPosLow/3 == cPosHigh/3 {
				// All instances of the number are in the same block.
				cb := cPosLow / 3 * 3
				rb := r / 3 * 3
				for ri := rb; ri < rb+3; ri++ {
					if ri == r {
						continue
					}
					for ci := cb; ci < cb+3; ci++ {
						bufferChan <- updateMsg{val, clear, ri, ci}
					}
				}
			}
		}
	}
	// Do some harder Sudoku solving.
	checkConstrainedSquares(unplacedValues, r, row, colPos)
	checkConstrainedValues(r, row)
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
				bufferChan <- updateMsg{val, set, rPos, c}
			}
		} else {
			// Check if all possible locations for the number are within the same block
			rPosLow := rowPos[val][0]
			rPosLast := len(rowPos[val]) - 1
			rPosHigh := rowPos[val][rPosLast]
			if rPosLow/3 == rPosHigh/3 {
				// All instances of the number are in the same block.
				rb := rPosLow / 3 * 3
				cb := c / 3 * 3
				for ci := cb; ci < cb+3; ci++ {
					if ci == c {
						continue
					}
					for ri := rb; ri < rb+3; ri++ {
						bufferChan <- updateMsg{val, clear, ri, ci}
					}
				}
			}
		}
	}
	// Do some harder Sudoku solving.
	checkConstrainedSquares(unplacedValues, c, column, rowPos)
	checkConstrainedValues(c, column)
}

func inspectBlock(r, c int) {
	type blockPosStruct struct {
		r int
		c int
	}
	unplacedValues := blank
	blockRowPos := make(map[squareVal][]blockPosStruct)
	blockColPos := make(map[squareVal][]blockPosStruct)
	rb := r / 3 * 3
	cb := c / 3 * 3
	// Count and locate each possible number in the remaining squares
	for val := one; val <= nine; val <<= 1 {
		for i := rb; i < rb+3; i++ {
			for j := cb; j < cb+3; j++ {
				if board[i][j].possVal&val == val {
					// square could be this value
					blockRowPos[val] = append(blockRowPos[val], blockPosStruct{i, j})
				}
			}
		}
		for j := cb; j < cb+3; j++ {
			for i := rb; i < rb+3; i++ {
				if board[i][j].possVal&val == val {
					// square could be this value
					blockColPos[val] = append(blockColPos[val], blockPosStruct{i, j})
				}
			}
		}
		if len(blockRowPos[val]) != len(blockColPos[val]) {
			panic("these should be equal")
		}
		if len(blockRowPos[val]) == 0 {
			panic("this is a problem, number not found in block")
		}
		// Check for previously unknown singletons in the block
		if len(blockRowPos[val]) == 1 {
			rPos := blockRowPos[val][0].r
			cPos := blockRowPos[val][0].c
			unplacedValues &^= val
			if !board[rPos][cPos].isFinal {
				bufferChan <- updateMsg{val, set, rPos, cPos}
			}
		} else {
			// Check if all possible locations for the number are within the same row or column
			blockPosLast := len(blockRowPos[val]) - 1
			rPosLow := blockRowPos[val][0].r
			rPosHigh := blockRowPos[val][blockPosLast].r
			cPosLow := blockColPos[val][0].c
			cPosHigh := blockColPos[val][blockPosLast].c
			if rPosLow == rPosHigh {
				// All possible locations of the number in this block are in the same row.
				for ri := rb; ri < rb+3; ri++ {
					if ri == rPosLow {
						continue
					}
					for ci := cb; ci < cb+3; ci++ {
						bufferChan <- updateMsg{val, clear, ri, ci}
					}
				}
			}
			if cPosLow == cPosHigh {
				// All possible locations of the number in this block are in the same column.
				for ci := cb; ci < cb+3; ci++ {
					if ci == cPosLow {
						continue
					}
					for ri := rb; ri < rb+3; ri++ {
						bufferChan <- updateMsg{val, clear, ri, ci}
					}
				}
			}
		}
	}
	blockPos := make(map[squareVal][]int)
	for val := one; val <= nine; val <<= 1 {
		for _, bp := range blockRowPos[val] {
			i := bp.r % 3
			j := bp.c % 3
			blockPos[val] = append(blockPos[val], 3*i+j)
		}
	}
	rb /= 3
	cb /= 3
	checkConstrainedSquares(unplacedValues, 3*rb+cb, block, blockPos)
	checkConstrainedValues(3*rb+cb, block)
}

func checkConstrainedSquares(unplacedValues squareVal, rcb int, isRCB rcbSelect, rcbPos map[squareVal][]int) {
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
				for _, i := range rcbPos[val1] {
					if cnt > 2 {
						break
					}
					if posMap&(1<<i) == 0 {
						posMap |= 1 << i
						posArray = append(posArray, i)
						cnt++
					}
				}
				for _, i := range rcbPos[val2] {
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
					switch isRCB {
					case row:
						bufferChan <- updateMsg{clearVal, clear, rcb, posArray[0]}
						bufferChan <- updateMsg{clearVal, clear, rcb, posArray[1]}
					case column:
						bufferChan <- updateMsg{clearVal, clear, posArray[0], rcb}
						bufferChan <- updateMsg{clearVal, clear, posArray[1], rcb}
					case block:
						rblock, cblock := rcb/3*3, rcb%3*3
						bufferChan <- updateMsg{clearVal, clear, rblock + posArray[0]/3, cblock + posArray[0]%3}
						bufferChan <- updateMsg{clearVal, clear, rblock + posArray[1]/3, cblock + posArray[0]%3}
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
					for _, i := range rcbPos[val1] {
						if cnt > 3 {
							break
						}
						if posMap&(1<<i) == 0 {
							posMap |= 1 << i
							posArray = append(posArray, i)
							cnt++
						}
					}
					for _, i := range rcbPos[val2] {
						if cnt > 3 {
							break
						}
						if posMap&(1<<i) == 0 {
							posMap |= 1 << i
							posArray = append(posArray, i)
							cnt++
						}
					}
					for _, i := range rcbPos[val3] {
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
						switch isRCB {
						case row:
							bufferChan <- updateMsg{clearVal, clear, rcb, posArray[0]}
							bufferChan <- updateMsg{clearVal, clear, rcb, posArray[1]}
							bufferChan <- updateMsg{clearVal, clear, rcb, posArray[2]}
						case column:
							bufferChan <- updateMsg{clearVal, clear, posArray[0], rcb}
							bufferChan <- updateMsg{clearVal, clear, posArray[1], rcb}
							bufferChan <- updateMsg{clearVal, clear, posArray[2], rcb}
						case block:
							rblock, cblock := rcb/3*3, rcb%3*3
							bufferChan <- updateMsg{clearVal, clear, rblock + posArray[0]/3, cblock + posArray[0]%3}
							bufferChan <- updateMsg{clearVal, clear, rblock + posArray[1]/3, cblock + posArray[1]%3}
							bufferChan <- updateMsg{clearVal, clear, rblock + posArray[2]/3, cblock + posArray[2]%3}
						}
					}
				}
			}
		}
	}
}

func checkConstrainedValues(rcb int, isRCB rcbSelect) {
	// If two squares can only hold the same two values and no others, then clear those values from the rest of the row, column or block.
	var pvCnt [9]int
	var sqrPaired [9]bool
	unresolvedCnt := 0

	blockpos := func(b, j int) (r, c int) {
		r = b/3*3 + j/3
		c = b%3*3 + j%3
		return
	}

	for j := 0; j < 9; j++ {
		switch isRCB {
		case row:
			pvCnt[j] = bits.OnesCount16(uint16(board[rcb][j].possVal))
		case column:
			pvCnt[j] = bits.OnesCount16(uint16(board[j][rcb].possVal))
		case block:
			r, c := blockpos(rcb, j)
			pvCnt[j] = bits.OnesCount16(uint16(board[r][c].possVal))
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
				var possVal1, possVal2 squareVal
				switch isRCB {
				case row:
					possVal1 = board[rcb][j1].possVal
					possVal2 = board[rcb][j2].possVal
				case column:
					possVal1 = board[j1][rcb].possVal
					possVal2 = board[j2][rcb].possVal
				case block:
					r1, c1 := blockpos(rcb, j1)
					r2, c2 := blockpos(rcb, j2)
					possVal1 = board[r1][c1].possVal
					possVal1 = board[r2][c2].possVal
				}
				if possVal1 == possVal2 {
					// We found a match of two squares that have the same two possible values. Clear those values from other squares in the row, column or block.
					sqrPaired[j1] = true
					sqrPaired[j2] = true
				loop2:
					for j := 0; j < 9; j++ {
						var r, c int
						if j == j1 || j == j2 {
							continue loop2
						}
						switch isRCB {
						case row:
							r, c = rcb, j
						case column:
							r, c = j, rcb
						case block:
							r, c = blockpos(rcb, j)
						}
						if board[r][c].isFinal {
							continue loop2
						}
						bufferChan <- updateMsg{possVal1, clear, r, c}
					}
				}
			}
		}
	}
	// If three squares can only hold the same three values and no others, then clear those values from the rest of the row, column or block.
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
					switch isRCB {
					case row:
						mergeVal = board[rcb][j1].possVal | board[rcb][j2].possVal | board[rcb][j3].possVal
					case column:
						mergeVal = board[j1][rcb].possVal | board[j2][rcb].possVal | board[j3][rcb].possVal
					case block:
						r1, c1 := blockpos(rcb, j1)
						r2, c2 := blockpos(rcb, j2)
						r3, c3 := blockpos(rcb, j3)
						mergeVal = board[r1][c1].possVal | board[r2][c2].possVal | board[r3][c3].possVal
					}
					if bits.OnesCount16(uint16(mergeVal)) == 3 {
						// Found a match of three unresolved squares that each have two or three of the same three possible values
					loop3:
						for j := 0; j < 9; j++ {
							var r, c int
							if j == j1 || j == j2 || j == j3 {
								continue loop3
							}
							switch isRCB {
							case row:
								r, c = rcb, j
							case column:
								r, c = j, rcb
							case block:
								r, c = blockpos(rcb, j)
							}
							if board[r][c].isFinal {
								continue loop3
							}
							bufferChan <- updateMsg{mergeVal, clear, r, c}
						}
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
				board[i][j].inChan <- updateMsg{intToVal[iv[j]], set, i, j}
				board[i][j].inChan <- updateMsg{action: pause}
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
