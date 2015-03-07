package main // import "github.com/mgood/docker-resolver"

// dnsmasq --no-daemon --no-hosts --addn-hosts our-hosts --resolv-file our-resolv
import (
	"errors"
	"io/ioutil"
	"log"
	"net"
	"os"
	"regexp"

	"github.com/mgood/docker-resolver/resolver"

	dockerapi "github.com/fsouza/go-dockerclient"
)

func getopt(name, def string) string {
	if env := os.Getenv(name); env != "" {
		return env
	}
	return def
}

func assert(err error) {
	if err != nil {
		log.Fatal("dns: ", err)
	}
}

func insertLine(line, path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	defer f.Close()

	orig, err := ioutil.ReadAll(f)
	if err != nil {
		return err
	}

	if _, err = f.Seek(0, 0); err != nil {
		return err
	}

	if _, err = f.WriteString(line + "\n"); err != nil {
		return err
	}

	_, err = f.Write(orig)
	return err
}

func removeLine(text, path string) error {
	patt := regexp.MustCompile("(?m:^" + regexp.QuoteMeta(text) + ")(?:$|\n)")

	orig, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(path, patt.ReplaceAllLiteral(orig, []byte{}), 0666)
	return err
}

func ipAddress() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && !ipnet.IP.IsMulticast() {
			if ipv4 := ipnet.IP.To4(); ipv4 != nil {
				return ipv4.String(), nil
			}
		}
	}

	return "", errors.New("no addresses found")
}

func registerContainers(docker *dockerapi.Client, dns resolver.Resolver) error {
	events := make(chan *dockerapi.APIEvents)
	if err := docker.AddEventListener(events); err != nil {
		return err
	}

	addContainer := func(containerId string) error {
		container, err := docker.InspectContainer(containerId)
		if err != nil {
			return err
		}
		addr := net.ParseIP(container.NetworkSettings.IPAddress)
		return dns.AddHost(containerId, addr, container.Config.Hostname, container.Name[1:])
	}

	containers, err := docker.ListContainers(dockerapi.ListContainersOptions{})
	if err != nil {
		return err
	}

	for _, listing := range containers {
		// TODO report errors adding containers?
		addContainer(listing.ID)
	}

	if err = dns.Listen(); err != nil {
		return err
	}
	defer dns.Close()

	for msg := range events {
		switch msg.Status {
		case "start":
			go addContainer(msg.ID)
		case "die":
			go dns.RemoveHost(msg.ID)
		}
	}

	return nil
}

func main() {
	docker, err := dockerapi.NewClient(getopt("DOCKER_HOST", "unix:///tmp/docker.sock"))
	assert(err)

	// address, err := ipAddress()
	// assert(err)
	// log.Println("got local address:", address)

	// resolveConf := getopt("RESOLV_CONF", "/tmp/resolv.conf")
	// resolveConfEntry := "nameserver " + address
	// assert(insertLine(resolveConfEntry, resolveConf))
	// defer removeLine(resolveConfEntry, resolveConf)

	dnsmasq := resolver.NewDnsmasqResolver()
	assert(registerContainers(docker, dnsmasq))

	log.Fatal("docker-resolver: docker event loop closed") // todo: reconnect?
}