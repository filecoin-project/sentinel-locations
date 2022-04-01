select
  distinct on (pac.miner_id) pac.height,
  pac.miner_id,
  m.peer_id
from
  power_actor_claims pac
join
  miner_infos m on m.miner_id = pac.miner_id
where
  pac.height between current_height() - 40320 and current_height()
  and m.peer_id != 'null'
order by
  pac.miner_id,
  pac.height desc
