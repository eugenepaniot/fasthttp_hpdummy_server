TARGETDIR = bin
BIN = hpdummy_server

all: $(TARGETDIR)/$(BIN)

$(TARGETDIR)/$(BIN): main.go
	go build -o $(TARGETDIR)/$(BIN) .

clean:
	rm -rfv $(TARGETDIR)
