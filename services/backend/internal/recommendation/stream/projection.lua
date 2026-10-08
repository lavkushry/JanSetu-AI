-- All keys share a Redis Cluster hash tag. Decimal versions stay strings so
-- bigint generations/versions remain exact above Lua's floating-point range.
local function compare(a,b)
 if string.len(a) ~= string.len(b) then return string.len(a) > string.len(b) and 1 or -1 end
 if a==b then return 0 end
 return a>b and 1 or -1
end
if ARGV[1]=='CONTENT' then
 local previous=redis.call('HGET',KEYS[1],'version')
 if previous and compare(ARGV[2],previous)<=0 then return 0 end
 redis.call('HSET',KEYS[1],'version',ARGV[2],'revision',ARGV[3],'eligible',ARGV[4])
 redis.call('EXPIRE',KEYS[1],7776000)
 return 1
end
local generation=redis.call('HGET',KEYS[1],'generation')
if generation and compare(ARGV[2],generation)<0 then return 0 end
if generation~=ARGV[2] or ARGV[3]~='true' then
 redis.call('DEL',KEYS[2],KEYS[3])
end
if redis.call('HGET',KEYS[1],'observationGeneration')~=ARGV[2] or ARGV[3]~='true' then
 redis.call('DEL',KEYS[4],KEYS[5])
end
redis.call('HSET',KEYS[1],'generation',ARGV[2],'enabled',ARGV[3])
redis.call('HSET',KEYS[1],'observationGeneration',ARGV[2],'observationSchema','1')
redis.call('EXPIRE',KEYS[1],7776000)
local now=redis.call('TIME')
if ARGV[1]=='CONTROL' or ARGV[3]~='true' or ARGV[4]~=ARGV[2] or tonumber(ARGV[10])<=tonumber(now[1])*1000+math.floor(tonumber(now[2])/1000) then return 0 end
-- Revision-aware observations have their own dedup set. Replaying a record
-- seen by an older scalar-only writer can fill the new projection safely.
if redis.call('HEXISTS',KEYS[5],ARGV[5])==0 and redis.call('HLEN',KEYS[5])<10000 then
 local previous=redis.call('HGET',KEYS[4],ARGV[13])
 if not previous or cjson.decode(previous)['order']<ARGV[11] then
  redis.call('HSET',KEYS[4],ARGV[13],ARGV[12])
  redis.call('HPEXPIREAT',KEYS[4],ARGV[10],'FIELDS',1,ARGV[13])
 end
 redis.call('HSET',KEYS[5],ARGV[5],1)
 redis.call('HPEXPIREAT',KEYS[5],ARGV[10],'FIELDS',1,ARGV[5])
 redis.call('EXPIRE',KEYS[4],2592000)
 redis.call('EXPIRE',KEYS[5],2592000)
end
if tonumber(ARGV[9])<=0 then return 0 end
if redis.call('HEXISTS',KEYS[3],ARGV[5])==1 then return 0 end
-- Bounded per-viewer state: beyond 10,000 actions/generation retain no new deltas.
if redis.call('HLEN',KEYS[3])>=10000 then return 2 end
redis.call('HSET',KEYS[3],ARGV[5],1)
redis.call('HPEXPIRE',KEYS[3],tonumber(ARGV[9])*1000,'FIELDS',1,ARGV[5])
local field=ARGV[6]..':'..ARGV[7]
local previousTTL=redis.call('HPTTL',KEYS[2],'FIELDS',1,field)[1]
if ARGV[6]=='READ' then
 -- Store the most recent normalized observation. Keeping an older maximum
 -- through newer reads would extend that older contribution's retention.
 if tonumber(ARGV[9])*1000>=previousTTL then
  redis.call('HSET',KEYS[2],field,ARGV[8])
 end
else
 redis.call('HSET',KEYS[2],field,1)
end
-- Each field expires independently; later unrelated activity never extends it.
redis.call('HPEXPIRE',KEYS[2],math.max(tonumber(ARGV[9])*1000,previousTTL),'FIELDS',1,field)
redis.call('EXPIRE',KEYS[2],2592000)
redis.call('EXPIRE',KEYS[3],2592000)
return 1
