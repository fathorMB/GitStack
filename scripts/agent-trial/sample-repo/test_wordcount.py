import unittest

from wordcount import count_words


class CountWordsTest(unittest.TestCase):
    def test_parole_semplici(self):
        self.assertEqual(count_words("uno due tre"), 3)

    def test_testo_vuoto(self):
        self.assertEqual(count_words(""), 0)

    def test_spazi_multipli(self):
        self.assertEqual(count_words("uno   due"), 2)

    def test_a_capo_e_tabulazioni(self):
        self.assertEqual(count_words("uno\ndue\ttre"), 3)


if __name__ == "__main__":
    unittest.main()
