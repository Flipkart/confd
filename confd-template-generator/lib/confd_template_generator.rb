require 'thor'
require 'confd/template/yaml_generator'

module Confd
  class ConfdTemplateGenerator < Thor
    desc "generate YAML_FILENAME", "generate tmpl, toml and json payload from a yaml FILE"
    options :name => :required, :type => :string
    options :dest => :required, :type => :string
    options :bucket => :required, :type => :string, :aliases => :b

    def generate(input)
      YamlGenerator.new(input, options[:name], options[:bucket], options[:dest]).generate
    end
  end
end
