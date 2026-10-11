`wordcount.py` conta male in due casi, e `python3 -m unittest -v` lo mostra:

- un testo vuoto (`""`) dà 1 invece di 0;
- spazi multipli (`"uno   due"`) danno 4 invece di 2.

Atteso: `count_words` conta le parole separate da qualsiasi spazio bianco
(spazi, a capo, tabulazioni) e dà 0 per un testo vuoto.

Criterio di chiusura: tutti i test di `test_wordcount.py` passano. Non servono
altre modifiche.
