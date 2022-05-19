# Pion -> pion -> livekit

1) Run livekit-sfu server
```sh
docker run --rm -p 7880:7880 -p 7881:7881 -p 7882:7882/udp -v $PWD/livekit.yaml:/livekit.yaml livekit/livekit-server --config /livekit.yaml --node-ip 0.0.0.0
```

2) Run subscriber
```sh
make subsriber
```

3) Run publisher
```sh
make publisher
```