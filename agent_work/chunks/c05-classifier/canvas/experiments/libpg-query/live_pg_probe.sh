D=/tmp/c05-08-pg
P() { psql -X -h $D -p 55438 -U postgres -d postgres "$@"; }
echo '--- SCS on (default):'; P -c "SELECT 'a\' ; --' ; SET search_path = x" ; P -c "SHOW search_path"
echo '--- SCS off via PGOPTIONS (startup packet):'; PGOPTIONS='-c standard_conforming_strings=off' P -c "SELECT 'a\' ; --' ; SET search_path = x; SHOW search_path"
echo '--- SET SCS off earlier in the same simple Query message, then trick:'; P -c "SET standard_conforming_strings = off; SELECT 'a\' ; --' ; SHOW search_path"
echo '--- syntax error later in message: does earlier SET run?'; P -c "SET search_path = x; SELECT 1); "
echo '--- E-string continuation probe:'; P -f $D/q.sql
echo '--- set_config SCS off via function in one message, then trick in same message:'; P -c "SELECT set_config('standard_conforming_strings','off',false); SELECT 'a\' ; --' ; SHOW search_path"
