CC ?= cc
CFLAGS ?= -O2 -std=c11 -Wall -Wextra

all: bench

bench: bench.c skiplist.h
	$(CC) $(CFLAGS) -o bench bench.c

test: test.c skiplist.h
	$(CC) $(CFLAGS) -o test test.c
	./test

clean:
	rm -f bench test

.PHONY: all clean test
