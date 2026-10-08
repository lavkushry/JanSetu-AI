-- One atomic read avoids combining features with a generation from a reset.
if redis.call('HGET',KEYS[1],'generation')~=ARGV[1] or
 redis.call('HGET',KEYS[1],'observationGeneration')~=ARGV[1] or
 redis.call('HGET',KEYS[1],'observationSchema')~='1' or
 redis.call('HGET',KEYS[1],'enabled')~='true' then return {} end
local result={}
for i=2,#ARGV do
 local value=redis.call('HGET',KEYS[2],ARGV[i])
 if value then
  if string.len(value)>512 then return redis.error_reply('invalid feature observation') end
  table.insert(result,ARGV[i])
  table.insert(result,value)
 end
end
table.insert(result,1,'1')
return result
