package main

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	"io/ioutil"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"net"
	"net/http"


	"github.com/manifoldco/promptui"
	"strconv"
)

func errorcheck(err error)  {
	fmt.Println(err)
}

func main() {
	prompt := promptui.Select{
		Label: "Select",
		Items: []string{"Post Forward Start", "Port Forward Stop", "New Port Rule"},
	}

	_, result, err := prompt.Run()

	errorcheck(err)

	if result == "Post Forward Start" {
		//port input
		validate := func(input string) error {
			_, err := strconv.ParseFloat(input, 64)
			return err
		}

		templates := &promptui.PromptTemplates{
			Valid:   "{{ . | green }} ",
			Prompt: "{{ . | red }} ",
		}

		prompt := promptui.Prompt{
			Label:     "Port?",
			Templates: templates,
			Validate:  validate,
		}
		port, err := prompt.Run()
		errorcheck(err)
		portInt, err := strconv.Atoi(port)
		errorcheck(err)

		//namespace input
		validate2 := func(input string) error {
			_, err := strconv.ParseFloat(input, 64)
			return err
		}

		templates2 := &promptui.PromptTemplates{
			Valid:   "{{ . | green }} ",
			Prompt: "{{ . | red }} ",
		}

		prompt2 := promptui.Prompt{
			Label:     "NameSpace?",
			Templates: templates2,
			Validate:  validate2,
		}
		_, err = prompt2.Run()
		errorcheck(err)
        //label input
		validate3 := func(input string) error {
			_, err := strconv.ParseFloat(input, 64)
			return err
		}

		templates3 := &promptui.PromptTemplates{
			Valid:   "{{ . | green }} ",
			Prompt: "{{ . | red }} ",
		}

		prompt3 := promptui.Prompt{
			Label:     "NameSpace?",
			Templates: templates3,
			Validate:  validate3,
		}
		_, err = prompt3.Run()
		errorcheck(err)

		fmt.Printf("You answered %s\n", portInt)
		//NewPortForwarder(namespace,___,portInt)

	}

}

type PortForward struct {
	Config *rest.Config
	Clientset kubernetes.Interface
	Name string
	Labels metav1.LabelSelector
	DestinationPort int
	ListenPort int
	Namespace string
	stopChan  chan struct{}
	readyChan chan struct{}
}


func NewPortForwarder(namespace string, labels metav1.LabelSelector, port int) (*PortForward, error) {
	pf := &PortForward{
		Namespace:       namespace,
		Labels:          labels,
		DestinationPort: port,
	}

	var err error
	pf.Config, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return pf, errors.Wrap(err, "Could not load kubernetes configuration file")
	}

	pf.Clientset, err = kubernetes.NewForConfig(pf.Config)
	if err != nil {
		return pf, errors.Wrap(err, "Could not create kubernetes client")
	}

	return pf, nil
}

func (p *PortForward) Start(ctx context.Context) error {
	p.stopChan = make(chan struct{}, 1)
	readyChan := make(chan struct{}, 1)
	errChan := make(chan error, 1)

	listenPort, err := p.getListenPort()
	if err != nil {
		return errors.Wrap(err, "Could not find a port to bind to")
	}

	dialer, err := p.dialer(ctx)
	if err != nil {
		return errors.Wrap(err, "Could not create a dialer")
	}

	ports := []string{
		fmt.Sprintf("%d:%d", listenPort, p.DestinationPort),
	}

	discard := ioutil.Discard
	pf, err := portforward.New(dialer, ports, p.stopChan, readyChan, discard, discard)
	if err != nil {
		return errors.Wrap(err, "Could not port forward into pod")
	}

	go func() {
		errChan <- pf.ForwardPorts()
	}()

	select {
	case err = <-errChan:
		return errors.Wrap(err, "Could not create port forward")
	case <-readyChan:
		return nil
	}

	return nil
}

// Stop a port forward.
func (p *PortForward) Stop() {
	p.stopChan <- struct{}{}
}

func (p *PortForward) getListenPort() (int, error) {
	var err error

	if p.ListenPort == 0 {
		p.ListenPort, err = p.getFreePort()
	}

	return p.ListenPort, err
}

func (p *PortForward) getFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}

	port := listener.Addr().(*net.TCPAddr).Port
	err = listener.Close()
	if err != nil {
		return 0, err
	}

	return port, nil
}

func (p *PortForward) dialer(ctx context.Context) (httpstream.Dialer, error) {
	pod, err := p.getPodName(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "Could not get pod name")
	}

	url := p.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(p.Namespace).
		Name(pod).
		SubResource("portforward").URL()

	transport, upgrader, err := spdy.RoundTripperFor(p.Config)
	if err != nil {
		return nil, errors.Wrap(err, "Could not create round tripper")
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", url)
	return dialer, nil
}

func (p *PortForward) getPodName(ctx context.Context) (string, error) {
	var err error
	if p.Name == "" {
		p.Name, err = p.findPodByLabels(ctx)
	}
	return p.Name, err
}

func (p *PortForward) findPodByLabels(ctx context.Context) (string, error) {
	if len(p.Labels.MatchLabels) == 0 && len(p.Labels.MatchExpressions) == 0 {
		return "", errors.New("No pod labels specified")
	}

	pods, err := p.Clientset.CoreV1().Pods(p.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(&p.Labels),
		FieldSelector: fields.OneTermEqualSelector("status.phase", string(v1.PodRunning)).String(),
	})

	if err != nil {
		return "", errors.Wrap(err, "Listing pods in kubernetes")
	}

	formatted := metav1.FormatLabelSelector(&p.Labels)

	if len(pods.Items) == 0 {
		return "", errors.New(fmt.Sprintf("Could not find running pod for selector: labels \"%s\"", formatted))
	}

	if len(pods.Items) != 1 {
		return "", errors.New(fmt.Sprintf("Ambiguous pod: found more than one pod for selector: labels \"%s\"", formatted))
	}

	return pods.Items[0].ObjectMeta.Name, nil
}


