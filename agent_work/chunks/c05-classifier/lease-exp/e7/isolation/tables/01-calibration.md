|                   quantity                    |                                condition                                |   n   |  p50_ns   |  p90_ns   |  p99_ns   |      source      |
|-----------------------------------------------|-------------------------------------------------------------------------|-------|-----------|-----------|-----------|------------------|
| reset, clean session                          | discard                                                                 | 15000 | 87659     | 126015    | 258837    | M1               |
| reset after one statement of kind             | advisory_lock                                                           | 900   | 175245    | 310967    | 1088523   | M2I discard      |
| reset after one statement of kind             | all                                                                     | 900   | 3049293   | 6652630   | 17236743  | M2I discard      |
| reset after one statement of kind             | cursor_hold                                                             | 900   | 164959    | 312993    | 972672    | M2I discard      |
| reset after one statement of kind             | custom_setting                                                          | 900   | 155762    | 288725    | 2072024   | M2I discard      |
| reset after one statement of kind             | failed_tx                                                               | 900   | 151294    | 289757    | 816363    | M2I discard      |
| reset after one statement of kind             | in_tx                                                                   | 900   | 132568    | 260581    | 942034    | M2I discard      |
| reset after one statement of kind             | listen                                                                  | 900   | 149385    | 263519    | 707848    | M2I discard      |
| reset after one statement of kind             | none                                                                    | 900   | 176974    | 351427    | 2068152   | M2I discard      |
| reset after one statement of kind             | prepare                                                                 | 900   | 162912    | 312186    | 709706    | M2I discard      |
| reset after one statement of kind             | set                                                                     | 900   | 164103    | 310663    | 1409759   | M2I discard      |
| reset after one statement of kind             | set_config_fn                                                           | 900   | 186210    | 357552    | 1442326   | M2I discard      |
| reset after one statement of kind             | set_role                                                                | 900   | 168596    | 327469    | 2568871   | M2I discard      |
| reset after one statement of kind             | setseed                                                                 | 900   | 172447    | 322534    | 969099    | M2I discard      |
| reset after one statement of kind             | temp_table                                                              | 900   | 3072418   | 6904213   | 24499728  | M2I discard      |
| reset after one statement of kind             | temp_table_rows                                                         | 900   | 3894448   | 7605922   | 27436552  | M2I discard      |
| reset after a lease ending in a transaction   | failed_tx                                                               | 900   | 340765    | 585459    | 2796300   | M2I total        |
| reset after a lease ending in a transaction   | in_tx                                                                   | 900   | 279579    | 524676    | 1723531   | M2I total        |
| connect                                       | c=1                                                                     | 900   | 16023942  | 21155553  | 37501555  | M3               |
| connect                                       | c=16                                                                    | 1920  | 53439291  | 69240527  | 82944207  | M3               |
| connect                                       | c=4                                                                     | 1920  | 21349304  | 30930224  | 45487451  | M3               |
| connect                                       | c=64                                                                    | 1920  | 153453964 | 239948846 | 285849809 | M3               |
| close until the backend is gone (upper bound) | poll                                                                    | 900   | 816070    | 1579471   | 3843576   | M4               |
| first statement pk_lookup                     | after_reset                                                             | 900   | 184802    | 262787    | 523051    | M5               |
| first statement pk_lookup                     | fresh                                                                   | 900   | 1750390   | 2091647   | 3658454   | M5               |
| first statement pk_lookup                     | reused                                                                  | 900   | 177865    | 256372    | 705765    | M5               |
| first statement select1                       | after_reset                                                             | 900   | 117309    | 197098    | 394982    | M5               |
| first statement select1                       | fresh                                                                   | 900   | 630535    | 850114    | 2796437   | M5               |
| first statement select1                       | reused                                                                  | 900   | 107054    | 158586    | 397806    | M5               |
| first statement plpgsql                       | after_reset                                                             | 900   | 611779    | 765374    | 1452625   | M6               |
| first statement plpgsql                       | fresh                                                                   | 900   | 4395443   | 5147230   | 7525247   | M6               |
| first statement plpgsql                       | reused                                                                  | 900   | 218473    | 309148    | 821121    | M6               |
| burst connect rate (per s)                    | B=100                                                                   | 14    | 246       | NULL      | NULL      | M7B uncontended  |
| burst connect rate (per s)                    | B=500                                                                   | 14    | 233       | NULL      | NULL      | M7B uncontended  |
| connect ceiling used by the report (per s)    | c=16                                                                    | NULL  | 195       | NULL      | NULL      | M7S              |
| sustained connect rate (per s)                | c=1                                                                     | 2     | 60        | NULL      | NULL      | M7S uncontended  |
| sustained connect rate (per s)                | c=16                                                                    | 3     | 195       | NULL      | NULL      | M7S uncontended  |
| sustained connect rate (per s)                | c=2                                                                     | 2     | 103       | NULL      | NULL      | M7S uncontended  |
| sustained connect rate (per s)                | c=32                                                                    | 3     | 197       | NULL      | NULL      | M7S uncontended  |
| sustained connect rate (per s)                | c=4                                                                     | 2     | 152       | NULL      | NULL      | M7S uncontended  |
| sustained connect rate (per s)                | c=64                                                                    | 3     | 192       | NULL      | NULL      | M7S uncontended  |
| sustained connect rate (per s)                | c=8                                                                     | 3     | 181       | NULL      | NULL      | M7S uncontended  |
| calibration id                                | sha256:bc6197988c0de9578534a59c5ac1bdbb80b17af929d9a2669a974360ca3e6edd | NULL  | NULL      | NULL      | NULL      | calibration.json |
| Postgres CPU per connect-and-close            | M7S uncontended                                                         | NULL  | 14287938  | NULL      | NULL      | cells            |
| client CPU per connect-and-close              | M7S uncontended                                                         | NULL  | 5754810   | NULL      | NULL      | cells            |
