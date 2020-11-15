# sudoku
The objective of this project is to write a go program that solves Sudoku puzzles, including the most difficult Sudokus found in the weekly newspaper,
employing the same techniques that a human uses to solve Sudokus.  Specifically, the program does not do a search, either DFS or BFS, of the possible
solution space.  Rather, at each step, it analyzes each row, column and 3x3 block and their combinations to deduce where it can next reduce the options
remaining, and applies those deductions to further resolve other squares in the puzzle.
The program is highly concurrent, using a number of go routines.  There is a go routine per square (81 in all), that operates as a monitor on each square.
The state of a square is only ever modified by its assigned go routine.  This avoids locking.  The program executes in a series of rounds.  In each round,
the square monitors first listen for inbound messages, which originate from other square monitors.  To avoid races, as the square monitors make deductions,
they send their output messages to a central channel, listened to by the round looper go routine.  Each square monitor will process incoming messages until 
it receives a pause message.  This indicates the end of a phase of a round.  At that point, it will indicate it is done to a wait group which is a barrier
across all 81 square monitors.  The round looper waits on that wait group.  When it can proceed, it forwards all the enqueued messages on its inbound channel
to the listening square monitors.  It then sends a pause message to each square monitor.  Upon completion and reaching the barrier again, it sends 27 messages to 
27 of the square monitors, selected somewhat arbitrarily from the 81 available square monitors.  Each of those messages will trigger the analysis of a row, a column
or a block.  This analysis looks for more complex scenarios typically found in more difficult Sudoku puzzles.  This results in additional messages sent by the
square monitors which are forwarded by the round looper to the targetted square monitors.
The state changing messages are set - set the square to a value - and clear - clear some possible values for the square.  The square value initially starts at
a specific number if it is one of the squares given as an initial condition in the puzzle, or as a set of all possible values (1..9) that the square may
eventually take.  As the puzzle is solved, this set is reduced using clear, or in some cases set, to a progressively smaller list of possibilities.  While
many options are possible for maintaining the set of possible values for each square, in this case a type is cast as a uint16, and the possible values for the square are stored as a bit vector, from
1<<0 to 1<<8 representing 1 to 9.

The logic to solve a puzzle can be divided into three basic groups:
1. When a group of n squares (n<9) in a structure (row, column or block) has been determined to have only n possible values among them, then no other square in that
structure can take on any of those values.  Those values are cleared.  The simplest case is when a single square is determined to have only one possible value, which
happens for example when all 8 other possible values are already existing among its neighbors in its row, column and or block.
2. When a group of n squares (n<9) in a structure (row, column or block) has been determined to be the only squares remaining in the structure where n values
can legally be placed.  The simplest case is when all other squares in the structure are determined to not be a designated value, then the square must take that
value.  This is the most common determination humans make when solving Sudokus.
3. When a value is determined to be restricted to a subset of a structure whose squares are all members of another structure, then that value cannot exist elsewhere
in the second structure.  For example, if within a row (column | block), the value 6 has been determined to be limited to 3 squares that are part of the same block,
then 6 cannot be placed elsewhere in that block.  Possible intersections of this type are row to block, block to row, column to block, block to column.

The code as written only applies rules 1 and 2 up to groupings of 3 squares in a structure.  This seems to be sufficient to solve the hardest weekly Sudokus, although
extending the code to handle more complex scenarios is certainly doable.
