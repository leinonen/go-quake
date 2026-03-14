PAK := $(HOME)/.wine/drive_c/GOG\ Games/Quake/id1

run:
	go run . -pak $(PAK) -map e1m1
