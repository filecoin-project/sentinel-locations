select
  distinct on (miner_id) height,
  miner_id,
  multi_addresses
from
  miner_infos
where
  multi_addresses != 'null' --json encoded stuff
order by
  miner_id,
  height desc
