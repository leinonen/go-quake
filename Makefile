PAK := $(HOME)/.wine/drive_c/GOG\ Games/Quake/id1/pak0.pak

run:
	go run . -pak $(PAK) -map e1m1
