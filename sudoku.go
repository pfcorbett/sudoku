// Sudoku.go
//
// This program will solve a Sudoku using the same techniques a human uses to solve Sudoku.  I.e., instead of doing a breadth first or depth first search of the possible
// solution space, it will at each step only commit numbers to squares when that number is provably correct.
// It uses channels and goroutines to constantly monitor as decisions are made and whether those decisions allow further choices to be made
package main

import (
	"fmt"
	"os"
)

const (
	one = 1 << iota
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

type square struct {
	possVal uint16
	inchan  chan uint16
}

var board [9][9]square

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Insufficient args, missing input filename.\n")
		os.Exit(1)
	}
	err := captureBoard(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	displayBoard()
}

func xlateSquare(s uint16) (c rune) {
	switch s {
	case one:
		c = '1'
	case two:
		c = '2'
	case three:
		c = '3'
	case four:
		c = '4'
	case five:
		c = '5'
	case six:
		c = '6'
	case seven:
		c = '7'
	case eight:
		c = '8'
	case nine:
		c = '9'
	case blank:
		c = ' '
	default:
		c = '?'
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
				switch iv[j] {
				case 1:
					board[i][j].possVal = one
				case 2:
					board[i][j].possVal = two
				case 3:
					board[i][j].possVal = three
				case 4:
					board[i][j].possVal = four
				case 5:
					board[i][j].possVal = five
				case 6:
					board[i][j].possVal = six
				case 7:
					board[i][j].possVal = seven
				case 8:
					board[i][j].possVal = eight
				case 9:
					board[i][j].possVal = nine
				case 0:
					board[i][j].possVal = blank
				default:
					panic("Should not get here")
				}
			}
		}
	}
	return nil
}

func displayBoard() {
	fmt.Println("\u250F\u2501\u2501\u2501\u252F\u2501\u2501\u2501\u252F\u2501\u2501\u2501\u2533\u2501\u2501\u2501\u252F\u2501\u2501\u2501" +
		"\u252F\u2501\u2501\u2501\u2533\u2501\u2501\u2501\u252F\u2501\u2501\u2501\u252F\u2501\u2501\u2501\u2513")
	for i := 0; i < 9; i++ {
		fmt.Printf("\u2503 %c \u2502 %c \u2502 %c \u2503 %c \u2502 %c \u2502 %c \u2503 %c \u2502 %c \u2502 %c \u2503\n",
			xlateSquare(board[i][0].possVal),
			xlateSquare(board[i][1].possVal),
			xlateSquare(board[i][2].possVal),
			xlateSquare(board[i][3].possVal),
			xlateSquare(board[i][4].possVal),
			xlateSquare(board[i][5].possVal),
			xlateSquare(board[i][6].possVal),
			xlateSquare(board[i][7].possVal),
			xlateSquare(board[i][8].possVal))
		if i == 2 || i == 5 {
			fmt.Println("\u2523\u2501\u2501\u2501\u253F\u2501\u2501\u2501\u253F\u2501\u2501\u2501\u254B\u2501\u2501\u2501\u253F\u2501\u2501\u2501" +
				"\u253F\u2501\u2501\u2501\u254B\u2501\u2501\u2501\u253F\u2501\u2501\u2501\u253F\u2501\u2501\u2501\u252B")
		} else if i == 8 {
			fmt.Println("\u2517\u2501\u2501\u2501\u2537\u2501\u2501\u2501\u2537\u2501\u2501\u2501\u253B\u2501\u2501\u2501\u2537\u2501\u2501\u2501" +
				"\u2537\u2501\u2501\u2501\u253B\u2501\u2501\u2501\u2537\u2501\u2501\u2501\u2537\u2501\u2501\u2501\u251B")
		} else {
			fmt.Println("\u2520\u2500\u2500\u2500\u253F\u2500\u2500\u2500\u253F\u2500\u2500\u2500\u2542\u2500\u2500\u2500\u253F\u2500\u2500" +
				"\u2500\u253F\u2500\u2500\u2500\u2542\u2500\u2500\u2500\u253F\u2500\u2500\u2500\u253F\u2500\u2500\u2500\u2528")
		}
	}
}
