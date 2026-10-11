"""Conta le parole di un testo."""

import sys


def count_words(text):
    """Restituisce il numero di parole del testo, separate da spazi."""
    return len(text.split(" "))


if __name__ == "__main__":
    print(count_words(sys.stdin.read()))
