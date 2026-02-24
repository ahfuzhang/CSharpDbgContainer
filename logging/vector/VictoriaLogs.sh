mkdir -p /home/ahfu/VictoriaLogsData/

docker run -d --rm --name victorialogs \
  --network="host" \
  --cpuset-cpus="4,5,6,7" \
  -m 2048m \
  -v /home/ahfu/VictoriaLogsData/:/data/ \
  -e GOMAXPROCS=4 \
  victoriametrics/victoria-logs:latest \
  -storageDataPath=/data/ \
  -inmemoryDataFlushInterval=30s \
  -memory.allowedPercent=80 \
  -retentionPeriod=3d


# http://192.168.1.248:9428/
# curl http://localhost:9428/select/logsql/streams -d 'query=*' -d 'start=1d' -d 'end=now'

