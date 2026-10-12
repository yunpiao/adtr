"""Offline drift guard for the backend-pinned Unicode search mapping."""
import os
from pathlib import Path
import shlex
import subprocess
import unittest


class DirectorySearchCaseMapContract(unittest.TestCase):
    def test_generated_table_matches_pinned_go(self):
        subprocess.run(
            [*shlex.split(os.environ.get('GO', 'go')), 'run',
             'scripts/generate_directory_search_case_map.go', '--check'],
            check=True, timeout=60,
        )
        generated = Path('web/src/directory-search-case-map.ts').read_bytes()
        self.assertLess(len(generated), 8192)


if __name__ == '__main__':
    unittest.main()
